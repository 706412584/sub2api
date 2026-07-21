package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	kiroprotocol "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// KiroGatewayService forwards Anthropic Messages requests through the native Kiro data plane.
type KiroGatewayService struct {
	httpUpstream        HTTPUpstream
	tlsFPProfileService *TLSFingerprintProfileService
	rateLimitService    *RateLimitService
	settingService      *SettingService
}

func NewKiroGatewayService(
	httpUpstream HTTPUpstream,
	tlsFPProfileService *TLSFingerprintProfileService,
	rateLimitService *RateLimitService,
	settingService *SettingService,
) *KiroGatewayService {
	return &KiroGatewayService{
		httpUpstream:        httpUpstream,
		tlsFPProfileService: tlsFPProfileService,
		rateLimitService:    rateLimitService,
		settingService:      settingService,
	}
}

func (s *KiroGatewayService) upstreamErrorBodyReadLimit() int64 {
	limit := int64(gatewayUpstreamErrorBodyReadLimit)
	if s != nil && s.settingService != nil && s.settingService.cfg != nil && s.settingService.cfg.Gateway.LogUpstreamErrorBody && s.settingService.cfg.Gateway.LogUpstreamErrorBodyMaxBytes > int(limit) {
		limit = int64(s.settingService.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
	}
	return limit
}

func (s *KiroGatewayService) readUpstreamErrorBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, s.upstreamErrorBodyReadLimit()))
	return body
}

func (s *KiroGatewayService) writeClaudeError(c *gin.Context, status int, errType, message string) error {
	MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"type":  "error",
		"error": gin.H{"type": errType, "message": message},
	})
	return fmt.Errorf("%s", message)
}

func (s *KiroGatewayService) buildCredentials(account *Account) kiroprotocol.Credentials {
	return kiroprotocol.Credentials{
		AccessToken:  strings.TrimSpace(account.GetCredential("access_token")),
		RefreshToken: strings.TrimSpace(account.GetCredential("refresh_token")),
		ProfileARN:   strings.TrimSpace(account.GetCredential("profile_arn")),
		APIKey:       strings.TrimSpace(account.GetCredential("kiro_api_key")),
		AuthMethod:   strings.TrimSpace(account.GetCredential("auth_method")),
		APIRegion:    account.GetKiroAPIRegion(),
		Endpoint:     kiroprotocol.Endpoint(account.GetKiroEndpoint()),
	}
}

func (s *KiroGatewayService) buildEndpointOptions(account *Account) kiroprotocol.EndpointOptions {
	machineID := strings.TrimSpace(account.GetCredential("machine_id"))
	if machineID == "" {
		machineID = "sub2api"
	}
	return kiroprotocol.EndpointOptions{
		Region:        account.GetKiroAPIRegion(),
		MachineID:     machineID,
		KiroVersion:   "0.7.1",
		SystemVersion: "windows",
		NodeVersion:   "20",
	}
}

// Forward handles Claude Messages JSON body against a Kiro OAuth/API Key account.
func (s *KiroGatewayService) Forward(ctx context.Context, c *gin.Context, account *Account, body []byte, _ bool) (*ForwardResult, error) {
	startTime := time.Now()

	var claudeReq kiroClaudeRequest
	if err := json.Unmarshal(body, &claudeReq); err != nil {
		return nil, s.writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}
	if strings.TrimSpace(claudeReq.Model) == "" {
		return nil, s.writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "Missing model")
	}

	originalModel := claudeReq.Model
	mappedModel := account.GetMappedModel(originalModel)
	if mappedModel == "" {
		mappedModel = originalModel
	}

	creds := s.buildCredentials(account)
	if err := creds.Validate(); err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: []byte(`{"type":"error","error":{"type":"authentication_error","message":"Invalid Kiro credentials"}}`),
			Platform:     PlatformKiro,
			Stage:        GatewayFailureStageAccountAuth,
			Scope:        GatewayFailureScopeAccount,
		}
	}

	// Optional restricted web-search agentic loop (native web_search tools only).
	if snap, used, loopErr := s.maybeWebSearchCollect(ctx, account, claudeReq, mappedModel); used {
		if loopErr != nil {
			return nil, loopErr
		}
		return s.renderCollectedClaude(c, snap, claudeReq.Stream, originalModel, mappedModel, startTime, false)
	}

	kiroReq, err := transformClaudeToKiro(claudeReq, mappedModel)
	if err != nil {
		return nil, s.writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}

	req, err := kiroprotocol.BuildDataPlaneRequest(creds, kiroReq, s.buildEndpointOptions(account))
	if err != nil {
		return nil, s.writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "Failed to build Kiro request")
	}
	req = req.WithContext(ctx)
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	var tlsProfile = (*tlsfingerprint.Profile)(nil)
	if s.tlsFPProfileService != nil {
		tlsProfile = s.tlsFPProfileService.ResolveTLSProfile(account)
	}
	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	if err != nil {
		if ctx.Err() != nil {
			return nil, s.writeClaudeError(c, http.StatusBadGateway, "api_error", "Client disconnected before upstream response")
		}
		return nil, &UpstreamFailoverError{
			StatusCode:             http.StatusBadGateway,
			ResponseBody:           []byte(`{"type":"error","error":{"type":"api_error","message":"Kiro upstream request failed"}}`),
			Platform:               PlatformKiro,
			RetryableOnSameAccount: true,
		}
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody := s.readUpstreamErrorBody(resp)
		return nil, s.mapHTTPError(account, resp.StatusCode, errBody)
	}

	if claudeReq.Stream {
		return s.handleStreaming(c, resp, startTime, originalModel, mappedModel)
	}
	return s.handleNonStreaming(c, resp, startTime, originalModel, mappedModel)
}

func (s *KiroGatewayService) mapHTTPError(account *Account, status int, body []byte) error {
	if kiroprotocol.IsQuotaExhausted(body) {
		return &UpstreamFailoverError{
			StatusCode:   status,
			ResponseBody: claudeErrorBody("rate_limit_error", "Kiro quota exhausted"),
			Platform:     PlatformKiro,
			Scope:        GatewayFailureScopeAccount,
		}
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 512 {
		msg = msg[:512]
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return &UpstreamFailoverError{
			StatusCode:   status,
			ResponseBody: claudeErrorBody("authentication_error", "Kiro authentication failed"),
			Platform:     PlatformKiro,
			Stage:        GatewayFailureStageAccountAuth,
			Scope:        GatewayFailureScopeAccount,
		}
	}
	if status == http.StatusTooManyRequests || status >= 500 {
		return &UpstreamFailoverError{
			StatusCode:             status,
			ResponseBody:           claudeErrorBody("api_error", fmt.Sprintf("Kiro upstream error: %d", status)),
			Platform:               PlatformKiro,
			RetryableOnSameAccount: status >= 500 || status == http.StatusTooManyRequests,
		}
	}
	// 4xx client validation: do not failover across accounts.
	return &UpstreamFailoverError{
		StatusCode:        status,
		ResponseBody:      claudeErrorBody("invalid_request_error", kiroFirstNonEmpty(msg, fmt.Sprintf("Kiro upstream error: %d", status))),
		Platform:          PlatformKiro,
		Scope:             GatewayFailureScopeRequest,
		NextAccountAction: NextAccountStop,
	}
}

func claudeErrorBody(errType, message string) []byte {
	b, _ := json.Marshal(map[string]any{
		"type":  "error",
		"error": map[string]string{"type": errType, "message": message},
	})
	return b
}

func kiroFirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// kiroClaudeRequest is a minimal Anthropic Messages subset used for Kiro transform.
type kiroClaudeRequest struct {
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	System   json.RawMessage `json:"system"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	Tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"input_schema"`
	} `json:"tools"`
}

func transformClaudeToKiro(req kiroClaudeRequest, mappedModel string) (kiroprotocol.Request, error) {
	if len(req.Messages) == 0 {
		return kiroprotocol.Request{}, fmt.Errorf("messages are required")
	}

	systemText := extractClaudeText(req.System)
	var history []kiroprotocol.Message
	var lastUserContent string
	var lastUserTools []kiroprotocol.Tool
	var lastUserToolResults []kiroprotocol.ToolResult
	var lastUserImages []kiroprotocol.Image

	// Convert all but the last user message into history; last user becomes current.
	for i, msg := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		text, toolUses, toolResults, images, err := extractClaudeContent(msg.Content)
		if err != nil {
			return kiroprotocol.Request{}, err
		}
		isLast := i == len(req.Messages)-1

		switch role {
		case "user":
			if isLast {
				lastUserContent = text
				if systemText != "" {
					if lastUserContent != "" {
						lastUserContent = systemText + "\n\n" + lastUserContent
					} else {
						lastUserContent = systemText
					}
				}
				lastUserToolResults = toolResults
				lastUserImages = images
				continue
			}
			content := text
			if i == 0 && systemText != "" {
				if content != "" {
					content = systemText + "\n\n" + content
				} else {
					content = systemText
				}
			}
			hm := &kiroprotocol.HistoryUserInputMessage{
				Content: content,
				ModelID: mappedModel,
				Origin:  kiroprotocol.OriginIDE,
				Images:  images,
			}
			if len(toolResults) > 0 {
				hm.UserInputMessageContext = &kiroprotocol.UserInputMessageContext{ToolResults: toolResults}
			}
			history = append(history, kiroprotocol.Message{UserInputMessage: hm})
		case "assistant":
			am := &kiroprotocol.AssistantMessage{Content: text, ToolUses: toolUses}
			history = append(history, kiroprotocol.Message{AssistantResponseMessage: am})
		default:
			// ignore unknown roles
		}
	}

	if lastUserContent == "" && len(lastUserToolResults) == 0 && len(lastUserImages) == 0 {
		// No explicit last user; take last message content.
		last := req.Messages[len(req.Messages)-1]
		var err error
		lastUserContent, _, lastUserToolResults, lastUserImages, err = extractClaudeContent(last.Content)
		if err != nil {
			return kiroprotocol.Request{}, err
		}
		if systemText != "" && lastUserContent != "" {
			lastUserContent = systemText + "\n\n" + lastUserContent
		}
	}

	for _, t := range req.Tools {
		// Server-side Anthropic tools (web_search_*) must not leak as client tools.
		if isKiroServerTool(t.Name) {
			continue
		}
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{}`)
		}
		lastUserTools = append(lastUserTools, kiroprotocol.Tool{
			ToolSpecification: kiroprotocol.ToolSpecification{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: kiroprotocol.InputSchema{JSON: schema},
			},
		})
	}

	out := kiroprotocol.NewRequest(uuid.NewString(), mappedModel, lastUserContent)
	out.ConversationState.History = history
	if len(lastUserImages) > 0 {
		out.ConversationState.CurrentMessage.UserInputMessage.Images = lastUserImages
	}
	ctxMsg := &out.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
	if len(lastUserTools) > 0 {
		ctxMsg.Tools = lastUserTools
	}
	if len(lastUserToolResults) > 0 {
		ctxMsg.ToolResults = lastUserToolResults
	}
	return out, nil
}

func extractClaudeText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var b strings.Builder
	for _, block := range blocks {
		typ, _ := block["type"].(string)
		if typ == "text" {
			if text, ok := block["text"].(string); ok {
				b.WriteString(text)
			}
		}
	}
	return b.String()
}

func extractClaudeContent(raw json.RawMessage) (text string, toolUses []kiroprotocol.ToolUse, toolResults []kiroprotocol.ToolResult, images []kiroprotocol.Image, err error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil, nil, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString, nil, nil, nil, nil
	}
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", nil, nil, nil, nil
	}
	var textParts []string
	for _, block := range blocks {
		typ, _ := block["type"].(string)
		switch typ {
		case "text":
			if t, ok := block["text"].(string); ok {
				textParts = append(textParts, t)
			}
		case "image":
			img, convErr := claudeImageToKiro(block)
			if convErr != nil {
				return "", nil, nil, nil, convErr
			}
			if img != nil {
				images = append(images, *img)
			}
		case "tool_use":
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			toolUses = append(toolUses, kiroprotocol.ToolUse{
				ToolUseID: id,
				Name:      name,
				Input:     block["input"],
			})
		case "tool_result":
			id, _ := block["tool_use_id"].(string)
			isError, _ := block["is_error"].(bool)
			content := ""
			switch v := block["content"].(type) {
			case string:
				content = v
			default:
				if b, err := json.Marshal(v); err == nil {
					content = string(b)
				}
			}
			if isError {
				toolResults = append(toolResults, kiroprotocol.ErrorToolResult(id, content))
			} else {
				toolResults = append(toolResults, kiroprotocol.SuccessToolResult(id, content))
			}
		}
	}
	return strings.Join(textParts, ""), toolUses, toolResults, images, nil
}

const kiroMaxImageBytes = 5 << 20 // 5 MiB decoded

func isKiroServerTool(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(n, "web_search") || n == "web_search" || strings.Contains(n, "web_search_")
}

func claudeImageToKiro(block map[string]any) (*kiroprotocol.Image, error) {
	source, _ := block["source"].(map[string]any)
	if source == nil {
		return nil, fmt.Errorf("image source is required")
	}
	srcType, _ := source["type"].(string)
	switch strings.ToLower(strings.TrimSpace(srcType)) {
	case "base64":
		mediaType, _ := source["media_type"].(string)
		data, _ := source["data"].(string)
		format, err := kiroImageFormat(mediaType)
		if err != nil {
			return nil, err
		}
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			// some clients omit padding
			raw, err = base64.RawStdEncoding.DecodeString(data)
			if err != nil {
				return nil, fmt.Errorf("invalid image base64")
			}
		}
		if len(raw) == 0 {
			return nil, fmt.Errorf("empty image data")
		}
		if len(raw) > kiroMaxImageBytes {
			return nil, fmt.Errorf("image exceeds %d bytes", kiroMaxImageBytes)
		}
		// re-encode to standard base64 without data URI for Kiro wire
		return &kiroprotocol.Image{
			Format: format,
			Source: kiroprotocol.ImageSource{Bytes: base64.StdEncoding.EncodeToString(raw)},
		}, nil
	case "url":
		// Avoid SSRF: require client to send base64; do not fetch remote URLs here.
		return nil, fmt.Errorf("image URL sources are not supported; use base64 image blocks")
	default:
		return nil, fmt.Errorf("unsupported image source type")
	}
}

func kiroImageFormat(mediaType string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/png", "png":
		return "png", nil
	case "image/jpeg", "image/jpg", "jpeg", "jpg":
		return "jpeg", nil
	case "image/webp", "webp":
		return "webp", nil
	case "image/gif", "gif":
		return "gif", nil
	default:
		return "", fmt.Errorf("unsupported image media type: %s", mediaType)
	}
}

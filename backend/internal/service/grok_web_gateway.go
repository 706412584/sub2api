package service

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Grok Web Gateway (mgw) 协议适配。
// 协议逻辑搬自参考项目 provider/web/gateway.go：
//
//	wss://grok.com/ws/mgw/?uid=<userID>
//	  -> session.create
//	  <- session.created + conversation.attached
//	  -> conversation.item.create + response.create
//	  <- response.chunk / response.output_text.delta ... / response.done

const (
	grokWebGatewayOrigin    = "https://grok.com"
	grokWebGatewayWSURL     = "wss://grok.com/ws/mgw/"
	grokWebSessionURL       = "https://grok.com/api/auth/session"
	grokWebHandshakeTimeout = 20 * time.Second
	grokWebHeartbeatPeriod  = 25 * time.Second
	grokWebResponseTimeout  = 5 * time.Minute
)

// grokWebIdentity 是一次请求所需的全部 Web 会话材料（解密后）。
type grokWebIdentity struct {
	SSO         string
	CFClearance string
	UserAgent   string
	UserID      string
}

func (g *grokWebIdentity) cookieHeader() string {
	parts := []string{"sso=" + g.SSO, "sso-rw=" + g.SSO}
	if g.CFClearance != "" {
		parts = append(parts, "cf_clearance="+g.CFClearance)
	}
	if g.UserID != "" {
		parts = append(parts, "x-userid="+g.UserID)
	}
	return strings.Join(parts, "; ")
}

// fetchGrokWebUserID 通过 /api/auth/session 解析 userId。
// 401/unauthenticated 表示 SSO 失效；403 通常为 clearance 失效。
func (s *OpenAIGatewayService) fetchGrokWebUserID(
	ctx context.Context, account *Account, identity *grokWebIdentity,
) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, grokWebSessionURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Cookie", strings.Join([]string{
		"sso=" + identity.SSO, "sso-rw=" + identity.SSO, "cf_clearance=" + identity.CFClearance,
	}, "; "))
	req.Header.Set("User-Agent", identity.UserAgent)
	req.Header.Set("Accept", "application/json")

	proxyURL := resolveAccountProxyURL(account)
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, 1)
	if err != nil {
		return "", fmt.Errorf("grok web session request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		_ = s.sessionCredentialService.MarkReauthRequired(ctx, account.ID, "web_session_unauthorized")
		return "", fmt.Errorf("grok web session unauthorized (%d), re-import required", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("grok web session endpoint returned %d", resp.StatusCode)
	}
	var payload struct {
		Status  string `json:"status"`
		Session struct {
			UserID string `json:"userId"`
		} `json:"session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode grok web session: %w", err)
	}
	if !strings.EqualFold(payload.Status, "authenticated") || payload.Session.UserID == "" {
		return "", fmt.Errorf("grok web session not authenticated")
	}
	return payload.Session.UserID, nil
}

type grokWebEnvelope struct {
	SessionID string `json:"session_id,omitempty"`
	Event     struct {
		Type     string          `json:"type"`
		Delta    string          `json:"delta,omitempty"`
		Text     string          `json:"text,omitempty"`
		Response json.RawMessage `json:"response,omitempty"`
		Output   json.RawMessage `json:"output,omitempty"`
		Chunk    *grokWebChunk   `json:"chunk,omitempty"`
	} `json:"event"`
}

type grokWebChunk struct {
	Text struct {
		Text    string `json:"text"`
		Channel string `json:"channel"`
	} `json:"text"`
}

// ForwardGrokWebChat 通过 mgw WebSocket 完成一次非流式对话，返回聚合文本。
// 流式/工具/搜索卡片的完整转换留待后续迭代；当前覆盖文本主链路。
func (s *OpenAIGatewayService) ForwardGrokWebChat(
	ctx context.Context,
	account *Account,
	model string,
	prompt string,
) (string, error) {
	material, err := s.sessionCredentialService.GetSessionMaterial(ctx, account.ID, account.ProxyID)
	if err != nil {
		return "", fmt.Errorf("grok web session material: %w", err)
	}
	identity := &grokWebIdentity{
		SSO: material.SSO, CFClearance: material.CFClearance, UserAgent: material.BrowserUA,
	}
	userID, err := s.fetchGrokWebUserID(ctx, account, identity)
	if err != nil {
		return "", err
	}
	identity.UserID = userID

	dialCtx, dialCancel := context.WithTimeout(ctx, grokWebHandshakeTimeout)
	defer dialCancel()
	headers := http.Header{}
	headers.Set("Origin", grokWebGatewayOrigin)
	headers.Set("User-Agent", identity.UserAgent)
	headers.Set("Cookie", identity.cookieHeader())
	headers.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Pragma", "no-cache")

	conn, _, _, err := s.getOpenAIWSPassthroughDialer().Dial(
		dialCtx, grokWebGatewayWSURL+"?uid="+userID, headers, resolveAccountProxyURL(account),
	)
	if err != nil {
		return "", fmt.Errorf("grok web gateway dial: %w", err)
	}
	defer func() { _ = conn.Close() }()

	requestCtx, cancel := context.WithTimeout(ctx, grokWebResponseTimeout)
	defer cancel()

	initialEventID := "evt_init_" + newGrokWebRequestUUID()
	session := map[string]any{
		"model": grokWebModelMode(model),
		"x_grok": map[string]any{
			"protocol_capabilities":   []string{"conversation_attached", "custom_methods_v1"},
			"use_chunk":               true,
			"enable_side_by_side":     true,
			"force_side_by_side":      false,
			"enable_image_generation": true,
			"image_generation_count":  2,
			"disable_text_follow_ups": false,
			"disable_artifact":        true,
			"force_concise":           false,
			"keep_context":            false,
			"is_temporary":            true,
			"disable_memory":          true,
		},
	}
	if err := writeGrokWebJSON(requestCtx, conn, map[string]any{
		"event": map[string]any{
			"type": "session.create", "event_id": initialEventID, "session": session,
		},
	}); err != nil {
		return "", fmt.Errorf("send session.create: %w", err)
	}

	heartbeatDone := make(chan struct{})
	defer close(heartbeatDone)
	go grokWebHeartbeat(requestCtx, conn, heartbeatDone)

	var (
		textParts   []string
		sessionID   string
		created     bool
		attached    bool
		turnSent    bool
		streamError string
	)
	for {
		if err := requestCtx.Err(); err != nil {
			return "", fmt.Errorf("grok web gateway timeout: %w", err)
		}
		data, err := conn.ReadMessage(requestCtx)
		if err != nil {
			if streamError != "" {
				return "", fmt.Errorf("grok web stream error: %s", streamError)
			}
			return "", fmt.Errorf("grok web read: %w", err)
		}
		var env grokWebEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		switch env.Event.Type {
		case "session.created":
			created = true
			if sessionID == "" {
				sessionID = env.SessionID
			}
		case "conversation.attached":
			attached = true
			var conv struct {
				Event struct {
					Conversation struct {
						ID string `json:"id"`
					} `json:"conversation"`
				} `json:"event"`
			}
			_ = json.Unmarshal(data, &conv)
			if sessionID == "" {
				sessionID = conv.Event.Conversation.ID
			}
		case "response.chunk":
			if env.Event.Chunk != nil && env.Event.Chunk.Text.Text != "" {
				textParts = append(textParts, env.Event.Chunk.Text.Text)
			}
		case "response.output_text.delta":
			if env.Event.Delta != "" {
				textParts = append(textParts, env.Event.Delta)
			}
		case "response.output_text.done":
			if len(textParts) == 0 && env.Event.Text != "" {
				textParts = append(textParts, env.Event.Text)
			}
		case "response.grok.output":
			var out struct {
				Event struct {
					Output struct {
						StreamError *struct {
							Kind    string `json:"kind"`
							Message string `json:"message"`
							Details struct {
								Reason string `json:"reason"`
							} `json:"details"`
						} `json:"stream_error"`
					} `json:"output"`
				} `json:"event"`
			}
			_ = json.Unmarshal(data, &out)
			if out.Event.Output.StreamError != nil {
				se := out.Event.Output.StreamError
				if se.Details.Reason == "entitlement" {
					streamError = fmt.Sprintf("model %q requires a higher Grok subscription tier", model)
				} else {
					streamError = se.Message
				}
			}
		case "response.done", "error":
			if streamError != "" {
				return "", fmt.Errorf("%s", streamError)
			}
			return strings.Join(textParts, ""), nil
		case "session.ended":
			return "", fmt.Errorf("grok web gateway session ended before response completed")
		}
		if created && attached && !turnSent && sessionID != "" {
			turnSent = true
			now := time.Now().UnixMilli()
			item := map[string]any{
				"session_id": sessionID,
				"event": map[string]any{
					"type":     "conversation.item.create",
					"event_id": fmt.Sprintf("evt_msg_%d", now),
					"item": map[string]any{
						"type": "message", "role": "user",
						"x_grok": map[string]any{
							"client_message_id": newGrokWebRequestUUID(),
							"input_chunks":      []any{map[string]any{"text": map[string]any{"text": prompt}}},
						},
					},
				},
			}
			if err := writeGrokWebJSON(requestCtx, conn, item); err != nil {
				return "", fmt.Errorf("send conversation.item.create: %w", err)
			}
			if err := writeGrokWebJSON(requestCtx, conn, map[string]any{
				"session_id": sessionID,
				"event":      map[string]any{"type": "response.create", "event_id": fmt.Sprintf("evt_resp_%d", now)},
			}); err != nil {
				return "", fmt.Errorf("send response.create: %w", err)
			}
		}
	}
}

func writeGrokWebJSON(ctx context.Context, conn openAIWSClientConn, value any) error {
	return conn.WriteJSON(ctx, value)
}

func grokWebHeartbeat(ctx context.Context, conn openAIWSClientConn, done <-chan struct{}) {
	ticker := time.NewTicker(grokWebHeartbeatPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-ticker.C:
			payload := map[string]any{
				"event": map[string]any{"type": "ping", "event_id": fmt.Sprintf("evt_hb_%d", now.UnixMilli())},
			}
			if err := writeGrokWebJSON(ctx, conn, payload); err != nil {
				_ = conn.Close()
				return
			}
		}
	}
}

func newGrokWebRequestUUID() string {
	b := make([]byte, 16)
	if _, err := crand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0F) | 0x40
	b[8] = (b[8] & 0x3F) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// grokWebPromptFromResponsesInput 从 OpenAI Responses input 提取纯文本 prompt。
// 支持 string 简写与 message/content 列表两种形态；工具调用等复杂项忽略。
func grokWebPromptFromResponsesInput(body []byte) string {
	input := gjson.GetBytes(body, "input")
	if !input.Exists() {
		return ""
	}
	if input.Type == gjson.String {
		return input.String()
	}
	items := input.Array()
	var parts []string
	appendContent := func(content gjson.Result) {
		for _, part := range content.Array() {
			if part.Get("type").Exists() && part.Get("type").String() != "output_text" && part.Get("type").String() != "input_text" && part.Get("type").String() != "text" {
				continue
			}
			if txt := part.Get("text").String(); txt != "" {
				parts = append(parts, txt)
			}
		}
	}
	for _, item := range items {
		switch item.Get("type").String() {
		case "", "message":
			appendContent(item.Get("content"))
		default:
			// reasoning / function_call / tool 输出等不进 Web prompt
		}
	}
	return strings.Join(parts, "\n")
}

// forwardGrokWebResponses 把 OpenAI Responses 请求转换为 Grok Web mgw 会话，
// 聚合文本后按 Responses JSON 形态返回。当前覆盖非流式文本主链路；
// 流式 SSE、工具调用与搜索卡片的完整转换在后续迭代接入。
func (s *OpenAIGatewayService) forwardGrokWebResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	originalModel string,
	reqStream bool,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	prompt := grokWebPromptFromResponsesInput(body)
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("grok web requires a non-empty text input")
	}

	text, err := s.ForwardGrokWebChat(ctx, account, originalModel, prompt)
	if err != nil {
		return nil, err
	}

	responseID := "resp_web_" + newGrokWebRequestUUID()
	now := time.Now().Unix()
	outputText := text

	payload := map[string]any{
		"id":                   responseID,
		"object":               "response",
		"created_at":           now,
		"status":               "completed",
		"model":                originalModel,
		"instructions":         nil,
		"parallel_tool_calls":  true,
		"tool_choice":          "auto",
		"tools":                []any{},
		"temperature":          nil,
		"top_p":                nil,
		"max_output_tokens":    gjson.GetBytes(body, "max_output_tokens").Int(),
		"previous_response_id": gjson.GetBytes(body, "previous_response_id").String(),
		"reasoning":            map[string]any{"effort": nil, "summary": nil},
		"text":                 map[string]any{"format": map[string]any{"type": "text"}},
		"store":                false,
		"background":           false,
		"metadata":             map[string]any{},
		"error":                nil,
		"incomplete_details":   nil,
		"usage": map[string]any{
			"input_tokens":          estimateWebPromptTokens(prompt),
			"output_tokens":         estimateWebOutputTokens(outputText),
			"total_tokens":          estimateWebPromptTokens(prompt) + estimateWebOutputTokens(outputText),
			"input_tokens_details":  map[string]any{"cached_tokens": 0},
			"output_tokens_details": map[string]any{"reasoning_tokens": 0},
		},
		"output": []any{
			map[string]any{
				"id":     "msg_" + responseID,
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"content": []any{
					map[string]any{"type": "output_text", "text": outputText, "annotations": []any{}},
				},
			},
		},
	}

	if reqStream {
		// 流式：以最小事件序列回放（created -> output_text.delta -> completed）。
		writeSSE := func(v any) error {
			data, err := json.Marshal(v)
			if err != nil {
				return err
			}
			_, err = c.Writer.WriteString("data: " + string(data) + "\n\n")
			return err
		}
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.WriteHeaderNow()

		baseEvent := map[string]any{
			"type":            "response.created",
			"response_id":     responseID,
			"sequence_number": 0,
		}
		_ = baseEvent
		createdPayload := map[string]any{"type": "response.created", "response": payload}
		if err := writeSSE(createdPayload); err != nil {
			return nil, err
		}
		deltaPayload := map[string]any{
			"type":          "response.output_text.delta",
			"item_id":       "msg_" + responseID,
			"output_index":  0,
			"content_index": 0,
			"delta":         outputText,
		}
		if err := writeSSE(deltaPayload); err != nil {
			return nil, err
		}
		donePayload := map[string]any{
			"type":            "response.completed",
			"response":        payload,
			"sequence_number": 3,
		}
		if err := writeSSE(donePayload); err != nil {
			return nil, err
		}
		_, _ = c.Writer.WriteString("data: [DONE]\n\n")
		c.Writer.Flush()
	} else {
		c.Header("Content-Type", "application/json")
		c.JSON(http.StatusOK, payload)
	}

	inTokens := estimateWebPromptTokens(prompt)
	outTokens := estimateWebOutputTokens(outputText)
	return &OpenAIForwardResult{
		RequestID:     responseID,
		ResponseID:    responseID,
		Model:         originalModel,
		UpstreamModel: originalModel,
		// mgw 上游不回传 model 字段；显式记录请求模型避免用量审计误判不一致。
		UpstreamResponseModel:         originalModel,
		UpstreamResponseModelConflict: false,
		Stream:                        reqStream,
		Duration:                      time.Since(startTime),
		Usage: OpenAIUsage{
			InputTokens:  inTokens,
			OutputTokens: outTokens,
		},
	}, nil
}

func estimateWebPromptTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	return (len(text) + 3) / 4
}

func estimateWebOutputTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	return (len(text) + 3) / 4
}

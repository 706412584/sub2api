package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	kiroprotocol "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ForwardAsChatCompletions converts OpenAI Chat Completions → Anthropic → Kiro,
// then renders Chat Completions JSON/SSE.
func (s *KiroGatewayService) ForwardAsChatCompletions(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	start := time.Now()
	var ccReq apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &ccReq); err != nil {
		return nil, s.writeChatError(c, http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}
	if strings.TrimSpace(ccReq.Model) == "" {
		return nil, s.writeChatError(c, http.StatusBadRequest, "invalid_request_error", "Missing model")
	}
	originalModel := ccReq.Model
	clientStream := ccReq.Stream

	responsesReq, err := apicompat.ChatCompletionsToResponses(&ccReq)
	if err != nil {
		return nil, s.writeChatError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	anthropicReq, err := apicompat.ResponsesToAnthropicRequest(responsesReq)
	if err != nil {
		return nil, s.writeChatError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	// Always collect upstream EventStream then re-render client format.
	anthropicReq.Stream = false
	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, s.writeChatError(c, http.StatusBadRequest, "invalid_request_error", "Failed to build request")
	}

	snap, mappedModel, err := s.executeAndCollect(ctx, account, anthropicBody, originalModel)
	if err != nil {
		return nil, err
	}

	anthropicResp := kiroSnapshotToAnthropic(snap, originalModel)
	responsesResp := apicompat.AnthropicToResponsesResponse(anthropicResp)
	ccResp := apicompat.ResponsesToChatCompletions(responsesResp, originalModel)
	usage := mapKiroUsage(snap)

	if clientStream {
		return s.writeChatStream(c, ccResp, usage, originalModel, mappedModel, start)
	}
	MarkResponseCommitted(c)
	c.JSON(http.StatusOK, ccResp)
	return &ForwardResult{
		RequestID:     ccResp.ID,
		Usage:         usage,
		Model:         originalModel,
		UpstreamModel: mappedModel,
		Stream:        false,
		Duration:      time.Since(start),
	}, nil
}

// ForwardAsResponses converts OpenAI Responses → Anthropic → Kiro,
// then renders Responses JSON (non-stream) or a minimal SSE lifecycle (stream).
func (s *KiroGatewayService) ForwardAsResponses(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	start := time.Now()
	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}
	if strings.TrimSpace(responsesReq.Model) == "" {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Missing model")
	}
	originalModel := responsesReq.Model
	clientStream := responsesReq.Stream

	anthropicReq, err := apicompat.ResponsesToAnthropicRequest(&responsesReq)
	if err != nil {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	anthropicReq.Stream = false
	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Failed to build request")
	}

	snap, mappedModel, err := s.executeAndCollect(ctx, account, anthropicBody, originalModel)
	if err != nil {
		return nil, err
	}

	anthropicResp := kiroSnapshotToAnthropic(snap, originalModel)
	responsesResp := apicompat.AnthropicToResponsesResponse(anthropicResp)
	responsesResp.Model = originalModel
	usage := mapKiroUsage(snap)

	if clientStream {
		return s.writeResponsesStream(c, responsesResp, usage, originalModel, mappedModel, start)
	}
	MarkResponseCommitted(c)
	c.JSON(http.StatusOK, responsesResp)
	return &ForwardResult{
		RequestID:     responsesResp.ID,
		Usage:         usage,
		Model:         originalModel,
		UpstreamModel: mappedModel,
		Stream:        false,
		Duration:      time.Since(start),
	}, nil
}

// executeAndCollect runs the Kiro data plane and aggregates the EventStream.
func (s *KiroGatewayService) executeAndCollect(ctx context.Context, account *Account, anthropicBody []byte, originalModel string) (kiroprotocol.Response, string, error) {
	var empty kiroprotocol.Response
	var claudeReq kiroClaudeRequest
	if err := json.Unmarshal(anthropicBody, &claudeReq); err != nil {
		return empty, "", &UpstreamFailoverError{
			StatusCode:        http.StatusBadRequest,
			ResponseBody:      claudeErrorBody("invalid_request_error", "Invalid converted request"),
			Platform:          PlatformKiro,
			Scope:             GatewayFailureScopeRequest,
			NextAccountAction: NextAccountStop,
		}
	}
	if claudeReq.Model == "" {
		claudeReq.Model = originalModel
	}
	mappedModel := account.GetMappedModel(claudeReq.Model)
	if mappedModel == "" {
		mappedModel = claudeReq.Model
	}

	creds := s.buildCredentials(account)
	if err := creds.Validate(); err != nil {
		return empty, "", &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: claudeErrorBody("authentication_error", "Invalid Kiro credentials"),
			Platform:     PlatformKiro,
			Stage:        GatewayFailureStageAccountAuth,
			Scope:        GatewayFailureScopeAccount,
		}
	}
	kiroReq, err := transformClaudeToKiro(claudeReq, mappedModel)
	if err != nil {
		return empty, "", &UpstreamFailoverError{
			StatusCode:        http.StatusBadRequest,
			ResponseBody:      claudeErrorBody("invalid_request_error", err.Error()),
			Platform:          PlatformKiro,
			Scope:             GatewayFailureScopeRequest,
			NextAccountAction: NextAccountStop,
		}
	}
	req, err := kiroprotocol.BuildDataPlaneRequest(creds, kiroReq, s.buildEndpointOptions(account))
	if err != nil {
		return empty, "", &UpstreamFailoverError{
			StatusCode:        http.StatusBadRequest,
			ResponseBody:      claudeErrorBody("invalid_request_error", "Failed to build Kiro request"),
			Platform:          PlatformKiro,
			Scope:             GatewayFailureScopeRequest,
			NextAccountAction: NextAccountStop,
		}
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
		return empty, "", &UpstreamFailoverError{
			StatusCode:             http.StatusBadGateway,
			ResponseBody:           claudeErrorBody("api_error", "Kiro upstream request failed"),
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
		return empty, "", s.mapHTTPError(account, resp.StatusCode, errBody)
	}
	result, err := kiroprotocol.CollectResponse(resp.Body, 0)
	if err != nil {
		if ue, ok := err.(*kiroprotocol.UpstreamError); ok {
			return empty, "", s.mapKiroUpstreamError(ue)
		}
		return empty, "", &UpstreamFailoverError{
			StatusCode:             http.StatusBadGateway,
			ResponseBody:           claudeErrorBody("api_error", "Kiro stream failed"),
			Platform:               PlatformKiro,
			RetryableOnSameAccount: true,
		}
	}
	return result, mappedModel, nil
}

func kiroSnapshotToAnthropic(snap kiroprotocol.Response, model string) *apicompat.AnthropicResponse {
	blocks := make([]apicompat.AnthropicContentBlock, 0, 2+len(snap.ToolUses))
	if snap.Thinking != "" {
		blocks = append(blocks, apicompat.AnthropicContentBlock{
			Type:      "thinking",
			Thinking:  snap.Thinking,
			Signature: snap.ThinkingSignature,
		})
	}
	if snap.Content != "" || len(snap.ToolUses) == 0 {
		blocks = append(blocks, apicompat.AnthropicContentBlock{Type: "text", Text: snap.Content})
	}
	for _, tool := range snap.ToolUses {
		input, _ := json.Marshal(tool.Input)
		blocks = append(blocks, apicompat.AnthropicContentBlock{
			Type:  "tool_use",
			ID:    tool.ToolUseID,
			Name:  tool.Name,
			Input: input,
		})
	}
	stop := mapKiroStopReason(snap.StopReason)
	usage := mapKiroUsage(snap)
	return &apicompat.AnthropicResponse{
		ID:         "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Type:       "message",
		Role:       "assistant",
		Model:      model,
		Content:    blocks,
		StopReason: apicompat.AnthropicStopReasonPtr(stop),
		Usage: apicompat.AnthropicUsage{
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
		},
	}
}

func (s *KiroGatewayService) writeChatError(c *gin.Context, status int, errType, message string) error {
	MarkResponseCommitted(c)
	c.JSON(status, gin.H{"error": gin.H{"type": errType, "message": message}})
	return fmt.Errorf("%s", message)
}

func (s *KiroGatewayService) writeResponsesError(c *gin.Context, status int, errType, message string) error {
	MarkResponseCommitted(c)
	c.JSON(status, gin.H{"error": gin.H{"type": errType, "message": message}})
	return fmt.Errorf("%s", message)
}

func (s *KiroGatewayService) writeChatStream(c *gin.Context, resp *apicompat.ChatCompletionsResponse, usage ClaudeUsage, model, mapped string, start time.Time) (*ForwardResult, error) {
	MarkResponseCommitted(c)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Flush()

	id := resp.ID
	if id == "" {
		id = "chatcmpl-" + uuid.NewString()
	}
	content := ""
	reasoning := ""
	var toolCalls []apicompat.ChatToolCall
	if len(resp.Choices) > 0 {
		content = rawJSONString(resp.Choices[0].Message.Content)
		reasoning = resp.Choices[0].Message.ReasoningContent
		toolCalls = resp.Choices[0].Message.ToolCalls
	}

	if reasoning != "" {
		writeSSEData(c, map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "reasoning_content": reasoning}, "finish_reason": nil}},
		})
	}
	if content != "" {
		writeSSEData(c, map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "content": content}, "finish_reason": nil}},
		})
	}
	if len(toolCalls) > 0 {
		writeSSEData(c, map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{"tool_calls": toolCalls}, "finish_reason": nil}},
		})
	}
	finish := "stop"
	if len(toolCalls) > 0 {
		finish = "tool_calls"
	}
	writeSSEData(c, map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": finish}},
		"usage": map[string]int{
			"prompt_tokens":     usage.InputTokens,
			"completion_tokens": usage.OutputTokens,
			"total_tokens":      usage.InputTokens + usage.OutputTokens,
		},
	})
	_, _ = io.WriteString(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()

	return &ForwardResult{
		RequestID:     id,
		Usage:         usage,
		Model:         model,
		UpstreamModel: mapped,
		Stream:        true,
		Duration:      time.Since(start),
	}, nil
}

func (s *KiroGatewayService) writeResponsesStream(c *gin.Context, resp *apicompat.ResponsesResponse, usage ClaudeUsage, model, mapped string, start time.Time) (*ForwardResult, error) {
	MarkResponseCommitted(c)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Flush()

	id := resp.ID
	if id == "" {
		id = "resp_" + uuid.NewString()
	}
	// Minimal lifecycle for clients that only need completed payload.
	writeSSEEvent(c, "response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": id, "status": "in_progress", "model": model}})
	writeSSEEvent(c, "response.in_progress", map[string]any{"type": "response.in_progress", "response": map[string]any{"id": id, "status": "in_progress", "model": model}})
	resp.Status = "completed"
	writeSSEEvent(c, "response.completed", map[string]any{"type": "response.completed", "response": resp})

	return &ForwardResult{
		RequestID:     id,
		Usage:         usage,
		Model:         model,
		UpstreamModel: mapped,
		Stream:        true,
		Duration:      time.Since(start),
	}, nil
}

func writeSSEData(c *gin.Context, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", data)
	c.Writer.Flush()
}

func writeSSEEvent(c *gin.Context, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data)
	c.Writer.Flush()
}

func rawJSONString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

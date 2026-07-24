package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	kiroprotocol "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (s *KiroGatewayService) handleNonStreaming(c *gin.Context, resp *http.Response, start time.Time, model, mappedModel string) (*ForwardResult, error) {
	result, err := kiroprotocol.CollectResponse(resp.Body, 0)
	if err != nil {
		if ue, ok := err.(*kiroprotocol.UpstreamError); ok {
			return nil, s.mapKiroUpstreamError(ue)
		}
		return nil, &UpstreamFailoverError{
			StatusCode:             http.StatusBadGateway,
			ResponseBody:           claudeErrorBody("api_error", "Kiro stream failed"),
			Platform:               PlatformKiro,
			RetryableOnSameAccount: true,
		}
	}

	content := buildClaudeContentBlocks(result)
	stopReason := mapKiroStopReason(result.StopReason)
	usage := mapKiroUsage(result)
	msgID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	payload := map[string]any{
		"id":            msgID,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       content,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]int{
			"input_tokens":                usage.InputTokens,
			"output_tokens":               usage.OutputTokens,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     0,
		},
	}
	MarkResponseCommitted(c)
	c.JSON(http.StatusOK, payload)

	duration := time.Since(start)
	return &ForwardResult{
		RequestID:     msgID,
		Usage:         usage,
		Model:         model,
		UpstreamModel: mappedModel,
		Stream:        false,
		Duration:      duration,
	}, nil
}

func (s *KiroGatewayService) handleStreaming(c *gin.Context, resp *http.Response, start time.Time, model, mappedModel string) (*ForwardResult, error) {
	MarkResponseCommitted(c)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()

	msgID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	state := kiroprotocol.NewResponseState(0)
	decoder := kiroprotocol.NewEventDecoder(resp.Body)

	// message_start
	writeClaudeSSE(c, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]int{
				"input_tokens":                0,
				"output_tokens":               0,
				"cache_creation_input_tokens": 0,
				"cache_read_input_tokens":     0,
			},
		},
	})

	textStarted := false
	thinkingStarted := false
	blockIndex := 0
	var firstTokenMs *int
	clientDisconnect := false

	for {
		event, err := decoder.ReadEvent()
		if err != nil {
			if err == io.EOF {
				break
			}
			if c.Request.Context().Err() != nil {
				clientDisconnect = true
				break
			}
			if ue, ok := err.(*kiroprotocol.UpstreamError); ok {
				// If we already started semantic content, just stop; otherwise failover.
				if textStarted || thinkingStarted || len(state.Snapshot().ToolUses) > 0 {
					break
				}
				return nil, s.mapKiroUpstreamError(ue)
			}
			if textStarted || thinkingStarted {
				break
			}
			return nil, &UpstreamFailoverError{
				StatusCode:             http.StatusBadGateway,
				ResponseBody:           claudeErrorBody("api_error", "Kiro stream failed"),
				Platform:               PlatformKiro,
				RetryableOnSameAccount: true,
			}
		}

		completedTool, applyErr := state.Apply(event)
		if applyErr != nil {
			if ue, ok := applyErr.(*kiroprotocol.UpstreamError); ok {
				if textStarted || thinkingStarted {
					break
				}
				return nil, s.mapKiroUpstreamError(ue)
			}
			if textStarted || thinkingStarted {
				break
			}
			return nil, &UpstreamFailoverError{
				StatusCode:   http.StatusBadGateway,
				ResponseBody: claudeErrorBody("api_error", applyErr.Error()),
				Platform:     PlatformKiro,
			}
		}

		if firstTokenMs == nil {
			switch event.Type {
			case kiroprotocol.EventAssistantResponse, kiroprotocol.EventReasoningContent, kiroprotocol.EventToolUse:
				ms := int(time.Since(start).Milliseconds())
				firstTokenMs = &ms
			}
		}

		switch event.Type {
		case kiroprotocol.EventAssistantResponse:
			if event.AssistantResponse == nil {
				continue
			}
			delta := event.AssistantResponse.Content
			if delta == "" {
				continue
			}
			if !textStarted {
				if thinkingStarted {
					writeClaudeSSE(c, "content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})
					blockIndex++
					thinkingStarted = false
				}
				writeClaudeSSE(c, "content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": blockIndex,
					"content_block": map[string]any{
						"type": "text",
						"text": "",
					},
				})
				textStarted = true
			}
			writeClaudeSSE(c, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": blockIndex,
				"delta": map[string]any{"type": "text_delta", "text": delta},
			})
		case kiroprotocol.EventReasoningContent:
			if event.ReasoningContent == nil {
				continue
			}
			delta := event.ReasoningContent.Text
			if delta == "" {
				continue
			}
			if !thinkingStarted {
				if textStarted {
					writeClaudeSSE(c, "content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})
					blockIndex++
					textStarted = false
				}
				writeClaudeSSE(c, "content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": blockIndex,
					"content_block": map[string]any{
						"type":     "thinking",
						"thinking": "",
					},
				})
				thinkingStarted = true
			}
			writeClaudeSSE(c, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": blockIndex,
				"delta": map[string]any{"type": "thinking_delta", "thinking": delta},
			})
		case kiroprotocol.EventToolUse:
			if completedTool == nil {
				// Stream partial input if tool start just appeared.
				if event.ToolUse != nil && !event.ToolUse.Stop && event.ToolUse.Name != "" {
					// open tool_use block on first chunk
					if textStarted || thinkingStarted {
						writeClaudeSSE(c, "content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})
						blockIndex++
						textStarted = false
						thinkingStarted = false
					}
					// We open tool block once per completedTool; partial open handled below when we first see tool.
				}
				continue
			}
			if textStarted || thinkingStarted {
				writeClaudeSSE(c, "content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})
				blockIndex++
				textStarted = false
				thinkingStarted = false
			}
			writeClaudeSSE(c, "content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": blockIndex,
				"content_block": map[string]any{
					"type":  "tool_use",
					"id":    completedTool.ToolUseID,
					"name":  completedTool.Name,
					"input": map[string]any{},
				},
			})
			inputJSON, _ := json.Marshal(completedTool.Input)
			writeClaudeSSE(c, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": blockIndex,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": string(inputJSON)},
			})
			writeClaudeSSE(c, "content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})
			blockIndex++
		}
	}

	_ = state.Finish()
	snapshot := state.Snapshot()
	if textStarted || thinkingStarted {
		writeClaudeSSE(c, "content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})
	}

	usage := mapKiroUsage(snapshot)
	stopReason := mapKiroStopReason(snapshot.StopReason)
	writeClaudeSSE(c, "message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]int{
			"output_tokens": usage.OutputTokens,
		},
	})
	writeClaudeSSE(c, "message_stop", map[string]any{"type": "message_stop"})

	return &ForwardResult{
		RequestID:        msgID,
		Usage:            usage,
		Model:            model,
		UpstreamModel:    mappedModel,
		Stream:           true,
		Duration:         time.Since(start),
		FirstTokenMs:     firstTokenMs,
		ClientDisconnect: clientDisconnect,
	}, nil
}

func (s *KiroGatewayService) mapKiroUpstreamError(ue *kiroprotocol.UpstreamError) error {
	if ue == nil {
		return &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: claudeErrorBody("api_error", "Kiro upstream error"),
			Platform:     PlatformKiro,
		}
	}
	if ue.QuotaExhausted() {
		return &UpstreamFailoverError{
			StatusCode:   http.StatusTooManyRequests,
			ResponseBody: claudeErrorBody("rate_limit_error", "Kiro quota exhausted"),
			Platform:     PlatformKiro,
			Scope:        GatewayFailureScopeAccount,
		}
	}
	if ue.ClientValidation() {
		return &UpstreamFailoverError{
			StatusCode:        http.StatusBadRequest,
			ResponseBody:      claudeErrorBody("invalid_request_error", kiroFirstNonEmpty(ue.Message, "Kiro client validation error")),
			Platform:          PlatformKiro,
			Scope:             GatewayFailureScopeRequest,
			NextAccountAction: NextAccountStop,
		}
	}
	if ue.BearerTokenInvalid() {
		return &UpstreamFailoverError{
			StatusCode:   http.StatusUnauthorized,
			ResponseBody: claudeErrorBody("authentication_error", "Kiro bearer token invalid"),
			Platform:     PlatformKiro,
			Stage:        GatewayFailureStageAccountAuth,
			Scope:        GatewayFailureScopeAccount,
		}
	}
	if ue.ContentLengthExceeded() {
		return &UpstreamFailoverError{
			StatusCode:        http.StatusBadRequest,
			ResponseBody:      claudeErrorBody("invalid_request_error", "Prompt is too long"),
			Platform:          PlatformKiro,
			Scope:             GatewayFailureScopeRequest,
			NextAccountAction: NextAccountStop,
		}
	}
	return &UpstreamFailoverError{
		StatusCode:             http.StatusBadGateway,
		ResponseBody:           claudeErrorBody("api_error", kiroFirstNonEmpty(ue.Message, "Kiro upstream error")),
		Platform:               PlatformKiro,
		RetryableOnSameAccount: true,
	}
}

func writeClaudeSSE(c *gin.Context, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data)
	c.Writer.Flush()
}

func buildClaudeContentBlocks(result kiroprotocol.Response) []map[string]any {
	var blocks []map[string]any
	if result.Thinking != "" {
		block := map[string]any{"type": "thinking", "thinking": result.Thinking}
		if result.ThinkingSignature != "" {
			block["signature"] = result.ThinkingSignature
		}
		blocks = append(blocks, block)
	}
	if result.Content != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": result.Content})
	}
	for _, tool := range result.ToolUses {
		blocks = append(blocks, map[string]any{
			"type":  "tool_use",
			"id":    tool.ToolUseID,
			"name":  tool.Name,
			"input": tool.Input,
		})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": ""})
	}
	return blocks
}

func mapKiroStopReason(reason string) string {
	switch reason {
	case "tool_use":
		return "tool_use"
	case "max_tokens", "model_context_window_exceeded":
		return "max_tokens"
	default:
		return "end_turn"
	}
}

func mapKiroUsage(result kiroprotocol.Response) ClaudeUsage {
	usage := ClaudeUsage{
		InputTokens: result.Usage.InputTokens,
	}
	// meteringEvent.usage is credits, not tokens. Estimate output from content length when needed.
	outChars := len(result.Content) + len(result.Thinking)
	for _, t := range result.ToolUses {
		if b, err := json.Marshal(t.Input); err == nil {
			outChars += len(b)
		}
	}
	if outChars > 0 {
		// rough 4 chars/token estimate; mark via zero cache fields only
		usage.OutputTokens = (outChars + 3) / 4
		if usage.OutputTokens == 0 {
			usage.OutputTokens = 1
		}
	}
	// without context window we cannot invert percentage from ContextUsagePercentage; leave InputTokens as 0

	return usage
}

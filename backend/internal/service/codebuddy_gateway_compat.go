// CodeBuddy 网关三条入站端点的转发实现。
//
// 转换链（以 /v1/messages 为例）：
//
//	Request:  Anthropic Messages → CC（apicompat.AnthropicToChatCompletionsRequest）
//	          → codebuddy.PrepareBody（强制流式 + 上游体改写）
//	Upstream: SSE（错误以 200 + data 帧内 error 对象表达）
//	Response: CC chunk → Anthropic events（直接桥接，不经 Responses 中间态）
//
// /v1/chat/completions：原生 CC 形状，逐帧 NormalizeFrame 透传（流式）；
// 非流式由聚合器产出标准 chat.completion。
// /v1/responses：CC chunk → Responses events（直接桥接）。
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Forward handles a Claude Messages body against a CodeBuddy account.
func (s *CodebuddyGatewayService) Forward(ctx context.Context, c *gin.Context, account *Account, body []byte, _ bool) (*ForwardResult, error) {
	startTime := time.Now()

	var anthropicReq apicompat.AnthropicRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		return nil, s.writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}
	originalModel := anthropicReq.Model
	if strings.TrimSpace(originalModel) == "" {
		return nil, s.writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
	}
	clientStream := anthropicReq.Stream

	if err := s.buildCredentials(account).Validate(); err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: claudeErrorBody("authentication_error", "Invalid CodeBuddy credentials"),
			Platform:     PlatformCodebuddy,
			Stage:        GatewayFailureStageAccountAuth,
			Scope:        GatewayFailureScopeAccount,
		}
	}
	// Anthropic → CC。
	chatReq, err := apicompat.AnthropicToChatCompletionsRequest(&anthropicReq)
	if err != nil {
		return nil, s.writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	upstreamModel := account.GetMappedModel(originalModel)
	if upstreamModel == "" {
		upstreamModel = originalModel
	}
	chatReq.Model = upstreamModel
	// 上游恒为流式（PrepareBody 强制 stream:true），include_usage 让末帧带用量。
	chatReq.Stream = true
	chatReq.StreamOptions = &apicompat.ChatStreamOptions{IncludeUsage: true}
	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		return nil, s.writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "Failed to build request")
	}
	chatBody = codebuddy.PrepareBody(chatBody, s.modelEfforts(account))

	resp, err := s.sendChat(ctx, c, account, chatBody)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody := s.readUpstreamErrorBody(resp)
		return nil, s.mapUpstreamError(resp.StatusCode, errBody)
	}

	requestID := resp.Header.Get("x-request-id")

	// 非流式：聚合上游 SSE → ChatCompletionsResponse → Anthropic JSON。
	// 与 CC 端点的非流式分支（ForwardAsChatCompletions）对称。
	if !clientStream {
		agg, aggErr := codebuddy.Aggregate(resp.Body)
		if aggErr != nil {
			if aggErr == codebuddy.ErrEmptyStream {
				return nil, &UpstreamFailoverError{
					StatusCode:             http.StatusBadGateway,
					ResponseBody:           claudeErrorBody("api_error", "CodeBuddy upstream returned an empty stream"),
					Platform:               PlatformCodebuddy,
					RetryableOnSameAccount: true,
				}
			}
			return nil, s.writeClaudeError(c, http.StatusBadGateway, "api_error", "Failed to read upstream response")
		}
		aggBody, _ := json.Marshal(agg)
		var ccResp apicompat.ChatCompletionsResponse
		if err := json.Unmarshal(aggBody, &ccResp); err != nil {
			return nil, s.writeClaudeError(c, http.StatusBadGateway, "api_error", "Failed to parse upstream response")
		}
		anthropicResp := apicompat.ChatCompletionsResponseToAnthropic(&ccResp, originalModel)
		if s.responseHeaderFilter != nil {
			// 过滤后的上游诊断头透传；Content-Type 必须显式覆盖——上游恒
			// text/event-stream，而这里回给客户端的是 JSON。
			writeFilteredResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		}
		MarkResponseCommitted(c)
		c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		c.JSON(http.StatusOK, anthropicResp)
		return &ForwardResult{
			RequestID:       requestID,
			UpstreamHeaders: resp.Header,
			Usage:           openAIUsageToClaudeUsage(extractCodebuddyAggregatedUsage(agg)),
			Model:           originalModel,
			UpstreamModel:   upstreamModel,
			Stream:          false,
			Duration:        time.Since(startTime),
		}, nil
	}

	writeStreamHeaders := s.newStreamHeaderWriter(c, resp.Header)
	anthropicState := apicompat.NewChatCompletionsToAnthropicStreamState(originalModel)
	clientDisconnected := false

	scan, streamErr := s.scanCodebuddyStream(c, resp, "codebuddy messages", requestID, startTime, func(chunk *apicompat.ChatCompletionsChunk) {
		anthropicEvents := apicompat.ChatCompletionsChunkToAnthropicEvents(chunk, anthropicState)
		if clientDisconnected {
			return
		}
		for _, aEvt := range anthropicEvents {
			sse, sseErr := apicompat.ResponsesAnthropicEventToSSE(aEvt)
			if sseErr != nil {
				continue
			}
			writeStreamHeaders()
			if _, wErr := fmt.Fprint(c.Writer, sse); wErr != nil {
				clientDisconnected = true
				break
			}
		}
		if !clientDisconnected && len(anthropicEvents) > 0 {
			c.Writer.Flush()
		}
	})

	// 流内业务错误帧（200 + code=11102 等）：转换成 failover 语义。
	if streamErr != nil {
		if c.Writer.Size() > 0 {
			// 已写过 SSE 头/内容，不可 failover——按终态错误收尾。
			MarkResponseCommitted(c)
			_, _ = fmt.Fprint(c.Writer, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"CodeBuddy upstream stream error\"}}\n\n")
			c.Writer.Flush()
			return &ForwardResult{
				RequestID: requestID, UpstreamHeaders: resp.Header,
				Usage: openAIUsageToClaudeUsage(scan.Usage), Model: originalModel, UpstreamModel: upstreamModel,
				Stream: true, Duration: time.Since(startTime), FirstTokenMs: scan.FirstTokenMs, ClientDisconnect: clientDisconnected,
			}, streamErr
		}
		return nil, streamErr
	}

	if scan.Err != nil {
		// 上游读断：跳过 finalize，避免合成 message_stop 掩盖截断。
		return &ForwardResult{
			RequestID: requestID, UpstreamHeaders: resp.Header,
			Usage: openAIUsageToClaudeUsage(scan.Usage), Model: originalModel, UpstreamModel: upstreamModel,
			Stream: true, Duration: time.Since(startTime), FirstTokenMs: scan.FirstTokenMs, ClientDisconnect: clientDisconnected,
		}, fmt.Errorf("stream usage incomplete: %w", scan.Err)
	}

	// Finalize：闭合未完块 + 补 message_delta / message_stop。
	finalEvents := apicompat.FinalizeChatCompletionsAnthropicStream(anthropicState)
	if !clientDisconnected {
		for _, aEvt := range finalEvents {
			sse, sseErr := apicompat.ResponsesAnthropicEventToSSE(aEvt)
			if sseErr != nil {
				continue
			}
			writeStreamHeaders()
			if _, wErr := fmt.Fprint(c.Writer, sse); wErr != nil {
				clientDisconnected = true
				break
			}
		}
		c.Writer.Flush()
	}
	if !scan.SawDone {
		logCCStreamMissingDoneSentinel("codebuddy messages", requestID)
	}

	// 上游恒流式；客户端请求非流式时标记 Stream=false 仅供计费口径参考。
	streamFlag := clientStream
	if !clientStream {
		streamFlag = false
	}
	return &ForwardResult{
		RequestID:        requestID,
		UpstreamHeaders:  resp.Header,
		Usage:            openAIUsageToClaudeUsage(scan.Usage),
		Model:            originalModel,
		UpstreamModel:    upstreamModel,
		Stream:           streamFlag,
		Duration:         time.Since(startTime),
		FirstTokenMs:     scan.FirstTokenMs,
		ClientDisconnect: clientDisconnected,
	}, nil
}

// ForwardAsChatCompletions serves a native OpenAI Chat Completions client.
// Streaming: frame-normalized passthrough. Non-streaming: upstream SSE is
// aggregated by codebuddy.Aggregate into a standard chat.completion.
func (s *CodebuddyGatewayService) ForwardAsChatCompletions(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	startTime := time.Now()

	var ccReq apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &ccReq); err != nil {
		return nil, s.writeChatError(c, http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}
	originalModel := ccReq.Model
	if strings.TrimSpace(originalModel) == "" {
		return nil, s.writeChatError(c, http.StatusBadRequest, "invalid_request_error", "Missing model")
	}
	clientStream := ccReq.Stream

	if err := s.buildCredentials(account).Validate(); err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: claudeErrorBody("authentication_error", "Invalid CodeBuddy credentials"),
			Platform:     PlatformCodebuddy,
			Stage:        GatewayFailureStageAccountAuth,
			Scope:        GatewayFailureScopeAccount,
		}
	}

	upstreamModel := account.GetMappedModel(originalModel)
	if upstreamModel == "" {
		upstreamModel = originalModel
	}
	chatBody, err := json.Marshal(ccReq)
	if err != nil {
		return nil, s.writeChatError(c, http.StatusBadRequest, "invalid_request_error", "Failed to build request")
	}
	chatBody = codebuddy.PrepareBody(chatBody, s.modelEfforts(account))
	// PrepareBody 已把 model 之外的形状改好；模型名替换在 marshal 前完成。
	if upstreamModel != originalModel {
		chatBody = replaceChatCompletionsBodyModel(chatBody, upstreamModel)
	}

	resp, err := s.sendChat(ctx, c, account, chatBody)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody := s.readUpstreamErrorBody(resp)
		return nil, s.mapUpstreamError(resp.StatusCode, errBody)
	}

	requestID := resp.Header.Get("x-request-id")

	if clientStream {
		// 原生 CC 流式：逐帧规范化透传（错误帧已由 Stream 内部处理不了——
		// 错误帧检测在扫描器里做，因此这里不用 codebuddy.Stream 而走扫描器，
		// 以获得 failover 语义与 usage 观测）。
		writeStreamHeaders := s.newStreamHeaderWriter(c, resp.Header)
		clientDisconnected := false
		var rawFrames []string
		scan, streamErr := s.scanCodebuddyStream(c, resp, "codebuddy chat completions", requestID, startTime, func(chunk *apicompat.ChatCompletionsChunk) {
			frame, mErr := json.Marshal(apicompatNormalizeChunk(chunk))
			if mErr != nil {
				return
			}
			rawFrames = append(rawFrames, string(frame))
			if clientDisconnected {
				return
			}
			writeStreamHeaders()
			if _, wErr := fmt.Fprintf(c.Writer, "data: %s\n\n", frame); wErr != nil {
				clientDisconnected = true
			}
		})
		_ = rawFrames
		if streamErr != nil {
			if c.Writer.Size() > 0 {
				MarkResponseCommitted(c)
				_, _ = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
				c.Writer.Flush()
				return &ForwardResult{
					RequestID: requestID, UpstreamHeaders: resp.Header,
					Usage: openAIUsageToClaudeUsage(scan.Usage), Model: originalModel, UpstreamModel: upstreamModel,
					Stream: true, Duration: time.Since(startTime), FirstTokenMs: scan.FirstTokenMs, ClientDisconnect: clientDisconnected,
				}, streamErr
			}
			return nil, streamErr
		}
		if scan.Err != nil {
			return &ForwardResult{
				RequestID: requestID, UpstreamHeaders: resp.Header,
				Usage: openAIUsageToClaudeUsage(scan.Usage), Model: originalModel, UpstreamModel: upstreamModel,
				Stream: true, Duration: time.Since(startTime), FirstTokenMs: scan.FirstTokenMs, ClientDisconnect: clientDisconnected,
			}, fmt.Errorf("stream usage incomplete: %w", scan.Err)
		}
		if !clientDisconnected {
			writeStreamHeaders()
			if _, wErr := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); wErr != nil {
				clientDisconnected = true
			}
			if !clientDisconnected {
				c.Writer.Flush()
			}
		}
		if !scan.SawDone {
			logCCStreamMissingDoneSentinel("codebuddy chat completions", requestID)
		}
		return &ForwardResult{
			RequestID:        requestID,
			UpstreamHeaders:  resp.Header,
			Usage:            openAIUsageToClaudeUsage(scan.Usage),
			Model:            originalModel,
			UpstreamModel:    upstreamModel,
			Stream:           true,
			Duration:         time.Since(startTime),
			FirstTokenMs:     scan.FirstTokenMs,
			ClientDisconnect: clientDisconnected,
		}, nil
	}

	// 非流式：聚合上游 SSE 为单个 chat.completion。
	agg, aggErr := codebuddy.Aggregate(resp.Body)
	if aggErr != nil {
		if aggErr == codebuddy.ErrEmptyStream {
			return nil, &UpstreamFailoverError{
				StatusCode:             http.StatusBadGateway,
				ResponseBody:           claudeErrorBody("api_error", "CodeBuddy upstream returned an empty stream"),
				Platform:               PlatformCodebuddy,
				RetryableOnSameAccount: true,
			}
		}
		return nil, s.writeChatError(c, http.StatusBadGateway, "api_error", "Failed to read upstream response")
	}
	agg["model"] = originalModel
	if s.responseHeaderFilter != nil {
		// 过滤后的上游诊断头透传；Content-Type 必须显式覆盖——上游恒
		// text/event-stream，而这里回给客户端的是 JSON。
		writeFilteredResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	MarkResponseCommitted(c)
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.JSON(http.StatusOK, agg)

	usage := openAIUsageToClaudeUsage(extractCodebuddyAggregatedUsage(agg))
	return &ForwardResult{
		RequestID:       requestID,
		UpstreamHeaders: resp.Header,
		Usage:           usage,
		Model:           originalModel,
		UpstreamModel:   upstreamModel,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

// ForwardAsResponses serves an OpenAI Responses client through the
// CodeBuddy Chat Completions data plane.
func (s *CodebuddyGatewayService) ForwardAsResponses(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	startTime := time.Now()

	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Invalid request body")
	}
	originalModel := responsesReq.Model
	if strings.TrimSpace(originalModel) == "" {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Missing model")
	}
	clientStream := responsesReq.Stream

	if err := s.buildCredentials(account).Validate(); err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: claudeErrorBody("authentication_error", "Invalid CodeBuddy credentials"),
			Platform:     PlatformCodebuddy,
			Stage:        GatewayFailureStageAccountAuth,
			Scope:        GatewayFailureScopeAccount,
		}
	}

	effectiveTools, err := apicompat.EffectiveResponsesTools(&responsesReq)
	if err != nil {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	customTools := apicompat.CustomToolNames(effectiveTools)
	functionTools := apicompat.FunctionToolNames(effectiveTools)
	toolSearch := apicompat.HasToolSearchTool(effectiveTools)
	namespaceTools := apicompat.NamespaceToolNames(effectiveTools)

	chatReq, err := apicompat.ResponsesToChatCompletionsRequest(&responsesReq)
	if err != nil {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	upstreamModel := account.GetMappedModel(originalModel)
	if upstreamModel == "" {
		upstreamModel = originalModel
	}
	chatReq.Model = upstreamModel
	chatReq.Stream = true
	chatReq.StreamOptions = &apicompat.ChatStreamOptions{IncludeUsage: true}
	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		return nil, s.writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Failed to build request")
	}
	chatBody = codebuddy.PrepareBody(chatBody, s.modelEfforts(account))

	logger.L().Debug("codebuddy responses: forwarding via chat completions",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
	)

	resp, err := s.sendChat(ctx, c, account, chatBody)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody := s.readUpstreamErrorBody(resp)
		return nil, s.mapUpstreamError(resp.StatusCode, errBody)
	}

	requestID := resp.Header.Get("x-request-id")

	if !clientStream {
		// 非流式：聚合 → CC 响应 → Responses 响应。
		agg, aggErr := codebuddy.Aggregate(resp.Body)
		if aggErr != nil {
			if aggErr == codebuddy.ErrEmptyStream {
				return nil, &UpstreamFailoverError{
					StatusCode:             http.StatusBadGateway,
					ResponseBody:           claudeErrorBody("api_error", "CodeBuddy upstream returned an empty stream"),
					Platform:               PlatformCodebuddy,
					RetryableOnSameAccount: true,
				}
			}
			return nil, s.writeResponsesError(c, http.StatusBadGateway, "api_error", "Failed to read upstream response")
		}
		aggBytes, mErr := json.Marshal(agg)
		if mErr != nil {
			return nil, s.writeResponsesError(c, http.StatusBadGateway, "api_error", "Failed to parse upstream response")
		}
		var ccResp apicompat.ChatCompletionsResponse
		if uErr := json.Unmarshal(aggBytes, &ccResp); uErr != nil {
			return nil, s.writeResponsesError(c, http.StatusBadGateway, "api_error", "Failed to parse upstream response")
		}
		responsesResp := apicompat.ChatCompletionsResponseToResponses(&ccResp, originalModel, customTools, functionTools, toolSearch, namespaceTools)
		if s.responseHeaderFilter != nil {
			writeFilteredResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		}
		MarkResponseCommitted(c)
		c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		c.JSON(http.StatusOK, responsesResp)
		return &ForwardResult{
			RequestID:       requestID,
			UpstreamHeaders: resp.Header,
			Usage:           openAIUsageToClaudeUsage(extractCodebuddyAggregatedUsage(agg)),
			Model:           originalModel,
			UpstreamModel:   upstreamModel,
			Stream:          false,
			Duration:        time.Since(startTime),
		}, nil
	}

	// 流式：CC chunk → Responses events。
	writeStreamHeaders := s.newStreamHeaderWriter(c, resp.Header)
	state := apicompat.NewChatCompletionsToResponsesStreamState(originalModel)
	state.CustomTools = customTools
	state.FunctionTools = functionTools
	state.ToolSearchDeclared = toolSearch
	state.NamespaceTools = namespaceTools
	clientDisconnected := false

	writeEvents := func(events []apicompat.ResponsesStreamEvent) {
		if clientDisconnected || len(events) == 0 {
			return
		}
		writeStreamHeaders()
		for _, event := range events {
			sse, sseErr := apicompat.ResponsesEventToSSE(event)
			if sseErr != nil {
				logger.L().Warn("codebuddy responses: failed to marshal stream event",
					zap.Error(sseErr),
					zap.String("request_id", requestID),
				)
				continue
			}
			if _, wErr := fmt.Fprint(c.Writer, sse); wErr != nil {
				clientDisconnected = true
				logger.L().Debug("codebuddy responses: client disconnected, continuing to drain upstream for billing",
					zap.Error(wErr),
					zap.String("request_id", requestID),
				)
				return
			}
		}
		c.Writer.Flush()
	}

	scan, streamErr := s.scanCodebuddyStream(c, resp, "codebuddy responses", requestID, startTime, func(chunk *apicompat.ChatCompletionsChunk) {
		events := apicompat.ChatCompletionsChunkToResponsesEvents(chunk, state)
		writeEvents(events)
	})

	if streamErr != nil {
		if c.Writer.Size() > 0 {
			MarkResponseCommitted(c)
			_, _ = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
			c.Writer.Flush()
			return &ForwardResult{
				RequestID: requestID, UpstreamHeaders: resp.Header,
				Usage: openAIUsageToClaudeUsage(scan.Usage), Model: originalModel, UpstreamModel: upstreamModel,
				Stream: true, Duration: time.Since(startTime), FirstTokenMs: scan.FirstTokenMs, ClientDisconnect: clientDisconnected,
			}, streamErr
		}
		return nil, streamErr
	}

	if scan.Err != nil {
		return &ForwardResult{
			RequestID: requestID, UpstreamHeaders: resp.Header,
			Usage: openAIUsageToClaudeUsage(scan.Usage), Model: originalModel, UpstreamModel: upstreamModel,
			Stream: true, Duration: time.Since(startTime), FirstTokenMs: scan.FirstTokenMs, ClientDisconnect: clientDisconnected,
		}, fmt.Errorf("stream usage incomplete: %w", scan.Err)
	}

	if err := state.ValidateToolCallArguments(); err != nil {
		return &ForwardResult{
			RequestID: requestID, UpstreamHeaders: resp.Header,
			Usage: openAIUsageToClaudeUsage(scan.Usage), Model: originalModel, UpstreamModel: upstreamModel,
			Stream: true, Duration: time.Since(startTime), FirstTokenMs: scan.FirstTokenMs, ClientDisconnect: clientDisconnected,
		}, fmt.Errorf("invalid tool call arguments from upstream: %w", err)
	}

	finalEvents := apicompat.FinalizeChatCompletionsResponsesStream(state)
	writeEvents(finalEvents)
	if !clientDisconnected {
		writeStreamHeaders()
		if _, wErr := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); wErr != nil {
			clientDisconnected = true
		}
		if !clientDisconnected {
			c.Writer.Flush()
		}
	}
	if !scan.SawDone {
		logCCStreamMissingDoneSentinel("codebuddy responses", requestID)
	}

	return &ForwardResult{
		RequestID:        requestID,
		UpstreamHeaders:  resp.Header,
		Usage:            openAIUsageToClaudeUsage(scan.Usage),
		Model:            originalModel,
		UpstreamModel:    upstreamModel,
		Stream:           true,
		Duration:         time.Since(startTime),
		FirstTokenMs:     scan.FirstTokenMs,
		ClientDisconnect: clientDisconnected,
	}, nil
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	kiroprotocol "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	kiroInternalWebSearchTool = "sub2api_web_search"
	kiroWebSearchMaxRounds    = 5
	kiroWebSearchMaxResults   = 5
	kiroWebSearchMaxResultLen = 12 << 10 // 12 KiB total tool result text
	kiroWebSearchLoopTimeout  = 45 * time.Second
)

// requestWantsKiroWebSearch reports whether the Claude request asked for native web_search.
func requestWantsKiroWebSearch(req kiroClaudeRequest) bool {
	for _, t := range req.Tools {
		if isKiroServerTool(t.Name) {
			return true
		}
	}
	return false
}

// injectKiroWebSearchTool adds an internal function tool so the model can request searches.
// Native Anthropic web_search tools remain filtered out of the client tool list.
func injectKiroWebSearchTool(tools []kiroprotocol.Tool) []kiroprotocol.Tool {
	for _, t := range tools {
		if t.ToolSpecification.Name == kiroInternalWebSearchTool {
			return tools
		}
	}
	schema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Search query"}},"required":["query"]}`)
	return append(tools, kiroprotocol.Tool{
		ToolSpecification: kiroprotocol.ToolSpecification{
			Name:        kiroInternalWebSearchTool,
			Description: "Search the public web and return concise results. Use for up-to-date facts.",
			InputSchema: kiroprotocol.InputSchema{JSON: schema},
		},
	})
}

func extractWebSearchQuery(input any) string {
	switch v := input.(type) {
	case string:
		var m map[string]any
		if err := json.Unmarshal([]byte(v), &m); err == nil {
			return strings.TrimSpace(fmt.Sprint(m["query"]))
		}
		return strings.TrimSpace(v)
	case map[string]any:
		if q, ok := v["query"]; ok {
			return strings.TrimSpace(fmt.Sprint(q))
		}
	case json.RawMessage:
		var m map[string]any
		if err := json.Unmarshal(v, &m); err == nil {
			return strings.TrimSpace(fmt.Sprint(m["query"]))
		}
	}
	// fallback: marshal then look for query
	b, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err == nil {
		return strings.TrimSpace(fmt.Sprint(m["query"]))
	}
	return ""
}

func formatKiroWebSearchResults(query string, resp *websearch.SearchResponse) string {
	if resp == nil || len(resp.Results) == 0 {
		return fmt.Sprintf("No web results for %q.", query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Web results for %q:\n", query)
	limit := len(resp.Results)
	if limit > kiroWebSearchMaxResults {
		limit = kiroWebSearchMaxResults
	}
	for i := 0; i < limit; i++ {
		r := resp.Results[i]
		fmt.Fprintf(&b, "%d. %s\n   %s\n   %s\n", i+1, strings.TrimSpace(r.Title), strings.TrimSpace(r.URL), strings.TrimSpace(r.Snippet))
		if b.Len() >= kiroWebSearchMaxResultLen {
			break
		}
	}
	out := b.String()
	if len(out) > kiroWebSearchMaxResultLen {
		out = out[:kiroWebSearchMaxResultLen] + "\n...[truncated]"
	}
	return out
}

func kiroWebSearchUses(tools []kiroprotocol.CompletedToolUse) []kiroprotocol.CompletedToolUse {
	var out []kiroprotocol.CompletedToolUse
	for _, t := range tools {
		if t.Name == kiroInternalWebSearchTool {
			out = append(out, t)
		}
	}
	return out
}

// runWebSearchAgenticLoop runs up to kiroWebSearchMaxRounds of:
// model tool_use(sub2api_web_search) -> local search -> tool_result -> model again.
// Semantic search content is produced only after at least one tool result is fed back.
func (s *KiroGatewayService) runWebSearchAgenticLoop(
	ctx context.Context,
	account *Account,
	base kiroprotocol.Request,
	mappedModel string,
) (kiroprotocol.Response, bool, error) {
	loopCtx, cancel := context.WithTimeout(ctx, kiroWebSearchLoopTimeout)
	defer cancel()

	req := base
	// Ensure internal tool is present on the current message.
	ctxMsg := &req.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
	ctxMsg.Tools = injectKiroWebSearchTool(ctxMsg.Tools)

	var last kiroprotocol.Response
	searchEmitted := false

	for round := 0; round < kiroWebSearchMaxRounds; round++ {
		if loopCtx.Err() != nil {
			return last, searchEmitted, loopCtx.Err()
		}
		snap, err := s.doKiroCollect(loopCtx, account, req)
		if err != nil {
			return last, searchEmitted, err
		}
		last = snap
		uses := kiroWebSearchUses(snap.ToolUses)
		if len(uses) == 0 {
			// Final answer (or non-search tools). Strip internal tool_use from snapshot if any leaked.
			return stripInternalWebSearchTools(snap), searchEmitted, nil
		}

		// Execute searches and append history: assistant tool_uses + user tool_results.
		var toolResults []kiroprotocol.ToolResult
		for _, use := range uses {
			query := extractWebSearchQuery(use.Input)
			if query == "" {
				toolResults = append(toolResults, kiroprotocol.ErrorToolResult(use.ToolUseID, "missing query"))
				continue
			}
			resp, _, searchErr := doWebSearch(loopCtx, account, query)
			if searchErr != nil {
				toolResults = append(toolResults, kiroprotocol.ErrorToolResult(use.ToolUseID, "search failed"))
				continue
			}
			searchEmitted = true
			toolResults = append(toolResults, kiroprotocol.SuccessToolResult(use.ToolUseID, formatKiroWebSearchResults(query, resp)))
		}

		// Move previous current user + assistant tool uses into history, then set new current user as tool results.
		prevUser := req.ConversationState.CurrentMessage.UserInputMessage
		histUser := &kiroprotocol.HistoryUserInputMessage{
			Content: prevUser.Content,
			ModelID: prevUser.ModelID,
			Origin:  prevUser.Origin,
			Images:  prevUser.Images,
		}
		if len(prevUser.UserInputMessageContext.Tools) > 0 || len(prevUser.UserInputMessageContext.ToolResults) > 0 {
			ctxCopy := prevUser.UserInputMessageContext
			histUser.UserInputMessageContext = &ctxCopy
		}
		req.ConversationState.History = append(req.ConversationState.History,
			kiroprotocol.Message{UserInputMessage: histUser},
			kiroprotocol.Message{AssistantResponseMessage: &kiroprotocol.AssistantMessage{
				Content:  snap.Content,
				ToolUses: toKiroToolUses(uses),
			}},
		)
		req.ConversationState.CurrentMessage.UserInputMessage = kiroprotocol.UserInputMessage{
			Content: "",
			ModelID: mappedModel,
			Origin:  prevUser.Origin,
			UserInputMessageContext: kiroprotocol.UserInputMessageContext{
				EnvState:    prevUser.UserInputMessageContext.EnvState,
				Tools:       injectKiroWebSearchTool(nil), // keep tool available
				ToolResults: toolResults,
			},
		}
	}
	// Exhausted rounds — return last snapshot without dangling internal tools.
	return stripInternalWebSearchTools(last), searchEmitted, nil
}

func toKiroToolUses(in []kiroprotocol.CompletedToolUse) []kiroprotocol.ToolUse {
	out := make([]kiroprotocol.ToolUse, 0, len(in))
	for _, t := range in {
		out = append(out, kiroprotocol.ToolUse{
			ToolUseID: t.ToolUseID,
			Name:      t.Name,
			Input:     t.Input,
		})
	}
	return out
}

func stripInternalWebSearchTools(snap kiroprotocol.Response) kiroprotocol.Response {
	if len(snap.ToolUses) == 0 {
		return snap
	}
	var kept []kiroprotocol.CompletedToolUse
	for _, t := range snap.ToolUses {
		if t.Name == kiroInternalWebSearchTool {
			continue
		}
		kept = append(kept, t)
	}
	snap.ToolUses = kept
	if snap.StopReason == "tool_use" && len(kept) == 0 {
		snap.StopReason = "end_turn"
	}
	return snap
}

// doKiroCollect builds and executes one data-plane request from a wire Request.
func (s *KiroGatewayService) doKiroCollect(ctx context.Context, account *Account, kiroReq kiroprotocol.Request) (kiroprotocol.Response, error) {
	var empty kiroprotocol.Response
	creds := s.buildCredentials(account)
	if err := creds.Validate(); err != nil {
		return empty, &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: claudeErrorBody("authentication_error", "Invalid Kiro credentials"),
			Platform:     PlatformKiro,
			Stage:        GatewayFailureStageAccountAuth,
			Scope:        GatewayFailureScopeAccount,
		}
	}
	req, err := kiroprotocol.BuildDataPlaneRequest(creds, kiroReq, s.buildEndpointOptions(account))
	if err != nil {
		return empty, &UpstreamFailoverError{
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
		return empty, &UpstreamFailoverError{
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
		return empty, s.mapHTTPError(account, resp.StatusCode, errBody)
	}
	result, err := kiroprotocol.CollectResponse(resp.Body, 0)
	if err != nil {
		if ue, ok := err.(*kiroprotocol.UpstreamError); ok {
			return empty, s.mapKiroUpstreamError(ue)
		}
		return empty, &UpstreamFailoverError{
			StatusCode:             http.StatusBadGateway,
			ResponseBody:           claudeErrorBody("api_error", "Kiro stream failed"),
			Platform:               PlatformKiro,
			RetryableOnSameAccount: true,
		}
	}
	return result, nil
}

// maybeWebSearchCollect runs the agentic loop when web_search is requested and a manager exists.
// Returns (response, usedLoop, error). usedLoop=false means caller should use normal path.
func (s *KiroGatewayService) maybeWebSearchCollect(
	ctx context.Context,
	account *Account,
	claudeReq kiroClaudeRequest,
	mappedModel string,
) (kiroprotocol.Response, bool, error) {
	var empty kiroprotocol.Response
	if !requestWantsKiroWebSearch(claudeReq) || getWebSearchManager() == nil {
		return empty, false, nil
	}
	kiroReq, err := transformClaudeToKiro(claudeReq, mappedModel)
	if err != nil {
		return empty, true, &UpstreamFailoverError{
			StatusCode:        http.StatusBadRequest,
			ResponseBody:      claudeErrorBody("invalid_request_error", err.Error()),
			Platform:          PlatformKiro,
			Scope:             GatewayFailureScopeRequest,
			NextAccountAction: NextAccountStop,
		}
	}
	snap, _, err := s.runWebSearchAgenticLoop(ctx, account, kiroReq, mappedModel)
	return snap, true, err
}

// renderCollectedClaude writes a collected snapshot as Claude Messages JSON/SSE.
func (s *KiroGatewayService) renderCollectedClaude(
	c *gin.Context,
	snap kiroprotocol.Response,
	stream bool,
	model, mappedModel string,
	start time.Time,
	clientDisconnect bool,
) (*ForwardResult, error) {
	if stream {
		// Synthesize a one-shot stream from the collected snapshot.
		MarkResponseCommitted(c)
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")
		c.Writer.Flush()

		msgID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		writeClaudeSSE(c, "message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": msgID, "type": "message", "role": "assistant", "model": model,
				"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]int{"input_tokens": 0, "output_tokens": 0, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0},
			},
		})
		idx := 0
		if snap.Thinking != "" {
			writeClaudeSSE(c, "content_block_start", map[string]any{"type": "content_block_start", "index": idx, "content_block": map[string]any{"type": "thinking", "thinking": ""}})
			writeClaudeSSE(c, "content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "thinking_delta", "thinking": snap.Thinking}})
			writeClaudeSSE(c, "content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
			idx++
		}
		if snap.Content != "" || len(snap.ToolUses) == 0 {
			writeClaudeSSE(c, "content_block_start", map[string]any{"type": "content_block_start", "index": idx, "content_block": map[string]any{"type": "text", "text": ""}})
			writeClaudeSSE(c, "content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "text_delta", "text": snap.Content}})
			writeClaudeSSE(c, "content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
			idx++
		}
		for _, tool := range snap.ToolUses {
			writeClaudeSSE(c, "content_block_start", map[string]any{"type": "content_block_start", "index": idx, "content_block": map[string]any{"type": "tool_use", "id": tool.ToolUseID, "name": tool.Name, "input": map[string]any{}}})
			inputJSON, _ := json.Marshal(tool.Input)
			writeClaudeSSE(c, "content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(inputJSON)}})
			writeClaudeSSE(c, "content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
			idx++
		}
		usage := mapKiroUsage(snap)
		writeClaudeSSE(c, "message_delta", map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": mapKiroStopReason(snap.StopReason), "stop_sequence": nil},
			"usage": map[string]int{"output_tokens": usage.OutputTokens},
		})
		writeClaudeSSE(c, "message_stop", map[string]any{"type": "message_stop"})
		return &ForwardResult{
			RequestID:        msgID,
			Usage:            usage,
			Model:            model,
			UpstreamModel:    mappedModel,
			Stream:           true,
			Duration:         time.Since(start),
			ClientDisconnect: clientDisconnect,
		}, nil
	}

	// Non-stream: reuse handleNonStreaming shape via synthetic body path.
	content := buildClaudeContentBlocks(snap)
	stopReason := mapKiroStopReason(snap.StopReason)
	usage := mapKiroUsage(snap)
	msgID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	payload := map[string]any{
		"id": msgID, "type": "message", "role": "assistant", "model": model,
		"content": content, "stop_reason": stopReason, "stop_sequence": nil,
		"usage": map[string]int{
			"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens,
			"cache_creation_input_tokens": 0, "cache_read_input_tokens": 0,
		},
	}
	MarkResponseCommitted(c)
	c.JSON(http.StatusOK, payload)
	return &ForwardResult{
		RequestID:     msgID,
		Usage:         usage,
		Model:         model,
		UpstreamModel: mappedModel,
		Stream:        false,
		Duration:      time.Since(start),
	}, nil
}

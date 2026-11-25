package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func turnLimitMessagesBody(t *testing.T, rounds int, stream bool) []byte {
	t.Helper()
	messages := []any{map[string]any{"role": "user", "content": "完成原始任务，并保留本地定制"}}
	for i := 0; i < rounds; i++ {
		id := fmt.Sprintf("call-%d", i)
		messages = append(messages,
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": "read_file", "input": map[string]any{"path": "source.go"}}}},
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": strings.Repeat("历史工具内容", 800)}}},
		)
	}
	body, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-6", "stream": stream, "max_tokens": 128,
		"system": "原始系统指令", "messages": messages,
		"tools":    []any{map[string]any{"name": "read_file", "input_schema": map[string]any{"type": "object"}}},
		"metadata": map[string]any{"custom": "must-preserve"},
	})
	require.NoError(t, err)
	return body
}

func TestGatewayTurnLimitPassthroughRecovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered", true: "stream"}[stream], func(t *testing.T) {
			body := turnLimitMessagesBody(t, 101, stream)
			refusal := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"session_turn_limit_exceeded","message":"reached 100 model turns"}}`))}
			payload := `{"id":"msg_ok","type":"message","role":"assistant","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":7,"output_tokens":3}}`
			contentType := "application/json"
			if stream {
				payload = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				contentType = "text/event-stream"
			}
			upstream := &httpUpstreamSequenceRecorder{responses: []*http.Response{refusal, {StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(payload))}}}
			cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
			svc := &GatewayService{cfg: cfg, httpUpstream: upstream, responseHeaderFilter: compileResponseHeaderFilter(cfg), rateLimitService: &RateLimitService{}, deferredService: &DeferredService{}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-6", Stream: stream}
			result, err := svc.Forward(context.Background(), c, newAnthropicAPIKeyAccountForTest(), parsed)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 2, upstream.callCount)
			require.Equal(t, 7, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
			require.Less(t, len(upstream.bodies[1]), len(upstream.bodies[0]))
			for _, field := range []string{"model", "system", "tools", "metadata", "stream"} {
				require.Equal(t, gjson.GetBytes(upstream.bodies[0], field).Raw, gjson.GetBytes(upstream.bodies[1], field).Raw, field)
			}
			require.Equal(t, upstream.bodies[1], parsed.Body.Bytes())
			require.NotContains(t, recorder.Body.String(), "session_turn_limit_exceeded")
		})
	}
}

func TestTurnLimitHistoryProtocolsAndBoundaries(t *testing.T) {
	for _, protocol := range []string{"anthropic", "responses", "chat"} {
		for _, rounds := range []int{99, 100, 101, 220} {
			t.Run(fmt.Sprintf("%s/%d", protocol, rounds), func(t *testing.T) {
				body := turnLimitProtocolBody(t, protocol, rounds)
				retry, ok := trimTurnLimitHistory(body, turnLimitHistoryBudget)
				require.True(t, ok)
				require.True(t, utf8.Valid(retry))
				require.Less(t, len(retry), len(body))
				path := "messages"
				if protocol == "responses" {
					path = "input"
				}
				before := gjson.GetBytes(body, path).Array()
				after := gjson.GetBytes(retry, path).Array()
				require.Equal(t, before[0].Raw, after[0].Raw, "任务锚点保持不变")
				require.Equal(t, before[len(before)-1].Raw, after[len(after)-1].Raw, "最新工具结果保持不变")
				var entries []turnLimitEntry
				visible := 0
				for _, item := range after {
					e, valid := parseTurnLimitEntry(json.RawMessage(item.Raw), protocol == "responses")
					require.True(t, valid)
					entries = append(entries, e)
					visible += e.bytes
				}
				_, valid := turnLimitCleanBoundaries(entries)
				require.True(t, valid, "所有工具调用和结果仍配对")
				require.LessOrEqual(t, visible, turnLimitHistoryBudget)
				for key, value := range gjson.ParseBytes(body).Map() {
					if key != path {
						require.Equal(t, value.Raw, gjson.GetBytes(retry, key).Raw, key)
					}
				}
			})
		}
	}
}

func turnLimitProtocolBody(t *testing.T, protocol string, rounds int) []byte {
	t.Helper()
	body := turnLimitMessagesBody(t, rounds, false)
	if protocol == "anthropic" {
		return body
	}
	items := []any{map[string]any{"role": "user", "content": "完成原始任务，并保留本地定制"}}
	for i := 0; i < rounds; i++ {
		id := fmt.Sprintf("call-%d", i)
		if protocol == "responses" {
			items = append(items,
				map[string]any{"type": "function_call", "call_id": id, "name": "read_file", "arguments": `{"path":"source.go"}`},
				map[string]any{"type": "function_call_output", "call_id": id, "output": strings.Repeat("历史工具内容", 800)},
			)
		} else {
			items = append(items,
				map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": id, "type": "function", "function": map[string]any{"name": "read_file", "arguments": `{"path":"source.go"}`}}}},
				map[string]any{"role": "tool", "tool_call_id": id, "content": strings.Repeat("历史工具内容", 800)},
			)
		}
	}
	path := "messages"
	if protocol == "responses" {
		path = "input"
		var err error
		body, err = sjson.DeleteBytes(body, "messages")
		require.NoError(t, err)
	}
	body, err := sjson.SetBytes(body, path, items)
	require.NoError(t, err)
	body, err = sjson.SetRawBytes(body, "unknown_extension", []byte(`{"precise":9007199254740993,"nested":["保留",true]}`))
	require.NoError(t, err)
	return body
}

func TestTurnLimitRetryGuards(t *testing.T) {
	body := turnLimitMessagesBody(t, 101, false)
	for _, tt := range []struct {
		name    string
		status  int
		errBody string
		setup   func(context.Context, *gin.Context, *turnLimitRetryState) context.Context
		want    bool
	}{
		{name: "100轮明确错误", status: 400, errBody: `{"error":{"code":"session_turn_limit_exceeded","message":"reached 100 model turns"}}`, want: true},
		{name: "200轮明确错误", status: 400, errBody: `{"code":"session_turn_limit_exceeded","message":"more than 200 model turns"}`, want: true},
		{name: "type兼容", status: 400, errBody: `{"error":{"type":"session_turn_limit_exceeded"}}`, want: true},
		{name: "无关400不重试", status: 400, errBody: `{"error":{"code":"invalid_request_error","message":"session_turn_limit_exceeded"}}`},
		{name: "429不重试", status: 429, errBody: `{"code":"session_turn_limit_exceeded"}`},
		{name: "截断JSON不重试", status: 400, errBody: `{"error":{"code":"session_turn_limit_exceeded"`},
		{name: "已用一次不重试", status: 400, errBody: `{"code":"session_turn_limit_exceeded"}`, setup: func(ctx context.Context, _ *gin.Context, state *turnLimitRetryState) context.Context {
			state.tried = true
			return ctx
		}},
		{name: "总预算耗尽", status: 400, errBody: `{"code":"session_turn_limit_exceeded"}`, setup: func(ctx context.Context, _ *gin.Context, state *turnLimitRetryState) context.Context {
			state.started = time.Now().Add(-maxRetryElapsed)
			return ctx
		}},
		{name: "请求取消", status: 400, errBody: `{"code":"session_turn_limit_exceeded"}`, setup: func(ctx context.Context, _ *gin.Context, _ *turnLimitRetryState) context.Context {
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			return cancelled
		}},
		{name: "客户端已取消", status: 400, errBody: `{"code":"session_turn_limit_exceeded"}`, setup: func(ctx context.Context, c *gin.Context, _ *turnLimitRetryState) context.Context {
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			c.Request = c.Request.WithContext(cancelled)
			return ctx
		}},
		{name: "已写响应", status: 400, errBody: `{"code":"session_turn_limit_exceeded"}`, setup: func(ctx context.Context, c *gin.Context, _ *turnLimitRetryState) context.Context {
			_, _ = c.Writer.Write([]byte("started"))
			return ctx
		}},
		{name: "已提交响应", status: 400, errBody: `{"code":"session_turn_limit_exceeded"}`, setup: func(ctx context.Context, c *gin.Context, _ *turnLimitRetryState) context.Context {
			c.Set(ResponseCommittedKey, true)
			return ctx
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			ctx := context.Background()
			state := turnLimitState(c, time.Now())
			require.Same(t, state, turnLimitState(c, time.Now()))
			if tt.setup != nil {
				ctx = tt.setup(ctx, c, state)
			}
			retry, ok := state.prepare(ctx, c, tt.status, body, []byte(tt.errBody))
			require.Equal(t, tt.want, ok)
			if ok {
				require.NotEmpty(t, retry)
				_, again := state.prepare(ctx, c, tt.status, retry, []byte(tt.errBody))
				require.False(t, again)
			}
		})
	}
}

func TestTurnLimitUnsafeHistoryUnchanged(t *testing.T) {
	body := turnLimitMessagesBody(t, 101, false)
	for _, tt := range []struct {
		name   string
		body   []byte
		budget int
	}{
		{"无历史", []byte(`{"model":"m","input":"hello"}`), 256},
		{"短历史", []byte(`{"messages":[{"role":"user","content":"hello"}]}`), 256},
		{"非法JSON", []byte(`{"messages":[`), 256},
		{"预算太小", body, 128},
		{"孤立结果", []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"missing","content":"` + strings.Repeat("a", 1000) + `"}]}]}`), 256},
		{"不完整调用", []byte(`{"input":[{"type":"function_call","call_id":"pending","name":"test","arguments":"` + strings.Repeat("a", 1000) + `"}]}`), 256},
		{"有状态续接", []byte(`{"previous_response_id":"resp_previous","messages":` + gjson.GetBytes(body, "messages").Raw + `}`), turnLimitHistoryBudget},
		{"有状态会话", []byte(`{"conversation":"conv_previous","messages":` + gjson.GetBytes(body, "messages").Raw + `}`), turnLimitHistoryBudget},
		{"项目引用", []byte(`{"input":[{"type":"item_reference","id":"old"},{"role":"user","content":"` + strings.Repeat("a", 1000) + `"}]}`), 256},
		{"中间系统指令", []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("a", 1000) + `"},{"role":"system","content":"keep"},{"role":"user","content":"latest"}]}`), 256},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original := append([]byte(nil), tt.body...)
			_, ok := trimTurnLimitHistory(tt.body, tt.budget)
			require.False(t, ok)
			require.Equal(t, original, tt.body)
		})
	}
}

func TestTurnLimitParallelToolBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name       string
		entries    []turnLimitEntry
		valid      bool
		boundaries []bool
	}{
		{"并行跨多结果", []turnLimitEntry{{calls: []string{"a", "b"}}, {results: []string{"a"}}, {results: []string{"b"}}, {user: true}}, true, []bool{true, false, false, true}},
		{"孤立结果", []turnLimitEntry{{results: []string{"a"}}}, false, nil},
		{"重复ID", []turnLimitEntry{{calls: []string{"a", "a"}}, {results: []string{"a"}}}, false, nil},
		{"重复结果", []turnLimitEntry{{calls: []string{"a"}}, {results: []string{"a", "a"}}}, false, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			boundaries, valid := turnLimitCleanBoundaries(tt.entries)
			require.Equal(t, tt.valid, valid)
			require.Equal(t, tt.boundaries, boundaries)
		})
	}
}

func TestTurnLimitServerToolHistory(t *testing.T) {
	for _, resultFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("result_first=%v", resultFirst), func(t *testing.T) {
			call := `{"type":"server_tool_use","id":"search-1","name":"web_search","input":{"query":"weather"}}`
			result := `{"type":"web_search_tool_result","tool_use_id":"search-1","content":[]}`
			blocks := call + "," + result
			if resultFirst {
				blocks = result + "," + call
			}
			body := []byte(`{"messages":[{"role":"user","content":"原始任务"},{"role":"assistant","content":"` + strings.Repeat("历史", 3000) + `"},{"role":"assistant","content":[` + blocks + `]},{"role":"user","content":"继续"}]}`)
			retry, ok := trimTurnLimitHistory(body, 4096)
			require.Equal(t, !resultFirst, ok)
			if ok {
				require.Contains(t, string(retry), call)
				require.Contains(t, string(retry), result)
			}
		})
	}
}

func TestTurnLimitOriginalTaskSurvivesManyUserMessages(t *testing.T) {
	messages := []any{map[string]any{"role": "user", "content": "ORIGINAL-TASK " + strings.Repeat("原始任务", 3000)}}
	for range 80 {
		messages = append(messages, map[string]any{"role": "user", "content": strings.Repeat("后续问题", 150)})
	}
	body, err := json.Marshal(map[string]any{"messages": messages})
	require.NoError(t, err)
	retry, ok := trimTurnLimitHistory(body, 8192)
	require.True(t, ok)
	require.Contains(t, string(retry), "ORIGINAL-TASK")
}

func TestTurnLimitResponsesRefusalCountsTowardBudget(t *testing.T) {
	body := []byte(`{"input":[{"role":"user","content":"原始任务"},{"role":"assistant","content":[{"type":"refusal","refusal":"` + strings.Repeat("拒绝", 3000) + `"}]}]}`)
	retry, ok := trimTurnLimitHistory(body, 4096)
	if ok {
		visible := 0
		for _, item := range gjson.GetBytes(retry, "input").Array() {
			if content := item.Get("content"); content.Type == gjson.String {
				visible += len(content.String())
			} else {
				for _, block := range content.Array() {
					visible += len(block.Get("text").String()) + len(block.Get("refusal").String())
				}
			}
		}
		require.LessOrEqual(t, visible, 4096)
	}
	entry, valid := parseTurnLimitEntry(json.RawMessage(gjson.GetBytes(body, "input.1").Raw), true)
	require.True(t, valid)
	require.Equal(t, len(strings.Repeat("拒绝", 3000)), entry.bytes)
}

type turnLimitCancellingContext struct {
	context.Context
	checks int
}

func (c *turnLimitCancellingContext) Err() error {
	c.checks++
	if c.checks > 1 {
		return context.Canceled
	}
	return nil
}

func TestTurnLimitRechecksCancellationAfterCompaction(t *testing.T) {
	state := &turnLimitRetryState{started: time.Now()}
	ctx := &turnLimitCancellingContext{Context: context.Background()}
	_, ok := state.prepare(ctx, nil, 400, turnLimitMessagesBody(t, 101, false), []byte(`{"code":"session_turn_limit_exceeded"}`))
	require.False(t, ok)
	require.False(t, state.tried)
}

func TestTurnLimitExcerptLargeString(t *testing.T) {
	large := json.RawMessage(`{"content":"` + strings.Repeat("历史𐐀", 200000) + `"}`)
	require.Equal(t, strings.Repeat("历史𐐀", 166)+"历史…[截断]", turnLimitExcerpt(large, 500))
	for _, limit := range []int{0, 1, 2, 3} {
		excerpt := turnLimitExcerpt(json.RawMessage(`{"content":[{"text":"历史𐐀"},{"text":"额外"}]}`), limit)
		require.True(t, utf8.ValidString(excerpt))
	}
}

func TestTurnLimitMediaAndLargeAnchor(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"ANCHOR ` + strings.Repeat("旧指令", 1000) + `"},{"role":"assistant","content":"done"},{"role":"user","content":[{"type":"text","text":"最新问题"},{"type":"image","source":{"type":"base64","data":"` + strings.Repeat("a", 20000) + `"}}]}],"max_tokens":9007199254740993}`)
	retry, ok := trimTurnLimitHistory(body, 4096)
	require.True(t, ok)
	require.True(t, utf8.Valid(retry))
	require.Contains(t, string(retry), "最新问题")
	require.Contains(t, string(retry), "历史媒体因中转会话容量限制已省略")
	require.Contains(t, string(retry), "ANCHOR")
	require.Equal(t, "9007199254740993", gjson.GetBytes(retry, "max_tokens").Raw)
	tool := json.RawMessage(`{"role":"assistant","content":[{"type":"tool_use","id":"a","input":{"type":"image","data":"business-value"}}]}`)
	unchanged, changed := turnLimitDegradeMedia(tool, false)
	require.False(t, changed)
	require.Equal(t, tool, unchanged)
}

func TestTurnLimitResponseReplayAndClosure(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		t.Run(fmt.Sprint(oversized), func(t *testing.T) {
			errBody := `{"code":"invalid_request_error"}`
			if oversized {
				errBody += strings.Repeat(" ", 70*1024)
			}
			original := &turnLimitTestReadCloser{Reader: strings.NewReader(errBody)}
			resp := &http.Response{StatusCode: 400, Body: original}
			state := &turnLimitRetryState{started: time.Now()}
			got, body, err := state.recoverResponse(context.Background(), nil, resp, []byte("untouched"), func([]byte) (*http.Response, []byte, error) { t.Fatal("不得重试"); return nil, nil, nil })
			require.NoError(t, err)
			require.Equal(t, "untouched", string(body))
			read, err := io.ReadAll(got.Body)
			require.NoError(t, err)
			require.Equal(t, errBody, string(read))
			require.False(t, original.closed)
			require.NoError(t, got.Body.Close())
			require.True(t, original.closed)
		})
	}
	t.Run("读取错误原样回放且不提前关闭", func(t *testing.T) {
		sentinel := errors.New("body read failed")
		errorBody := `{"code":"session_turn_limit_exceeded"}`
		original := &turnLimitTestReadCloser{Reader: io.MultiReader(strings.NewReader(errorBody), iotest.ErrReader(sentinel))}
		resp := &http.Response{StatusCode: http.StatusBadRequest, Body: original}
		state := &turnLimitRetryState{started: time.Now()}
		_, ok := state.prepareResponse(context.Background(), nil, resp, turnLimitMessagesBody(t, 101, false))
		require.False(t, ok)
		require.False(t, state.tried)
		require.False(t, original.closed)
		read, err := io.ReadAll(resp.Body)
		require.Equal(t, errorBody, string(read))
		require.ErrorIs(t, err, sentinel)
		require.NoError(t, resp.Body.Close())
		require.True(t, original.closed)
	})
	sentinel := errors.New("transport failed")
	original := &turnLimitTestReadCloser{Reader: strings.NewReader(`{"code":"session_turn_limit_exceeded"}`)}
	state := &turnLimitRetryState{started: time.Now()}
	_, _, err := state.recoverResponse(context.Background(), nil, &http.Response{StatusCode: 400, Body: original}, turnLimitMessagesBody(t, 101, false), func(retry []byte) (*http.Response, []byte, error) {
		require.True(t, original.closed)
		return nil, retry, sentinel
	})
	require.ErrorIs(t, err, sentinel)
	require.True(t, state.tried)
}

type turnLimitTestReadCloser struct {
	io.Reader
	closed bool
}

func (r *turnLimitTestReadCloser) Close() error { r.closed = true; return nil }

func TestGatewayTurnLimitAnthropicEntrypoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, ingress := range []string{"messages", "responses", "chat"} {
		for _, refusalCode := range []string{turnLimitErrorCode, "invalid_request_error"} {
			t.Run(ingress+"/"+refusalCode, func(t *testing.T) {
				protocol := ingress
				if protocol == "messages" {
					protocol = "anthropic"
				}
				body := turnLimitProtocolBody(t, protocol, 101)
				upstream := &httpUpstreamSequenceRecorder{responses: []*http.Response{
					{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"error":{"code":%q,"message":"reached 100 model turns"}}`, refusalCode)))},
					{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"session_turn_limit_exceeded","message":"still rejected"}}`))},
				}}
				cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
				svc := &GatewayService{cfg: cfg, httpUpstream: upstream, responseHeaderFilter: compileResponseHeaderFilter(cfg), rateLimitService: &RateLimitService{}, deferredService: &DeferredService{}, settingService: NewSettingService(&gatewayTTLSettingRepo{}, cfg)}
				account := newAnthropicAPIKeyAccountForTest()
				account.Extra["anthropic_passthrough"] = false
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+ingress, nil)
				parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-6"}
				var err error
				switch ingress {
				case "messages":
					_, err = svc.Forward(context.Background(), c, account, parsed)
				case "responses":
					_, err = svc.ForwardAsResponses(context.Background(), c, account, body, parsed)
				case "chat":
					_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, parsed)
				}
				require.Error(t, err)
				want := 1
				if refusalCode == turnLimitErrorCode {
					want = 2
				}
				require.Equal(t, want, upstream.callCount)
				if want == 2 {
					require.Less(t, len(upstream.bodies[1]), len(upstream.bodies[0]))
				}
			})
		}
	}
}

func TestTurnLimitSuccessfulResponseDoesNotTouchHistory(t *testing.T) {
	body := turnLimitMessagesBody(t, 101, false)
	original := append([]byte(nil), body...)
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("success"))}
	state := &turnLimitRetryState{started: time.Now()}
	got, wireBody, err := state.recoverResponse(context.Background(), nil, resp, body, func([]byte) (*http.Response, []byte, error) { t.Fatal("正常请求不重试"); return nil, nil, nil })
	require.NoError(t, err)
	require.Same(t, resp, got)
	require.Equal(t, original, wireBody)
	require.Equal(t, original, body)
	require.False(t, state.tried)
}

func TestTurnLimitOpenAIEntrypointsRetryExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name     string
		ingress  string
		protocol string
	}{
		{"原生Responses", "responses", APIProtocolResponses},
		{"Responses透传", "passthrough", APIProtocolResponses},
		{"原生Chat", "chat", APIProtocolChatCompletions},
		{"Messages转Responses", "messages", APIProtocolResponses},
		{"Chat转Responses", "chat", APIProtocolResponses},
		{"Messages原生桥", "messages", APIProtocolAnthropic},
		{"Responses转Anthropic", "responses", APIProtocolAnthropic},
		{"Chat转Anthropic", "chat", APIProtocolAnthropic},
	} {
		t.Run(tt.name, func(t *testing.T) {
			protocol := tt.ingress
			if protocol == "messages" {
				protocol = "anthropic"
			}
			if protocol == "passthrough" {
				protocol = "responses"
			}
			body := turnLimitProtocolBody(t, protocol, 101)
			body, err := sjson.SetBytes(body, "model", "test-model")
			require.NoError(t, err)
			upstream := &httpUpstreamSequenceRecorder{responses: []*http.Response{
				{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"session_turn_limit_exceeded","message":"reached 100 model turns"}}`))},
				{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"session_turn_limit_exceeded","message":"still rejected"}}`))},
			}}
			cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}}, Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, responseHeaderFilter: compileResponseHeaderFilter(cfg), rateLimitService: &RateLimitService{}}
			account := &Account{ID: 801, Name: "turn-limit-test", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "test-only-key", "base_url": "https://relay.example.test", "api_protocol": tt.protocol}, Extra: map[string]any{"openai_responses_supported": true}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+tt.ingress, nil)
			switch tt.ingress {
			case "responses":
				_, err = svc.Forward(context.Background(), c, account, body)
			case "passthrough":
				account.Extra["openai_passthrough"] = true
				_, err = svc.Forward(context.Background(), c, account, body)
			case "chat":
				_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
			case "messages":
				_, err = svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
			}
			require.Error(t, err, "第二次拒绝必须返回原错误链")
			require.Equal(t, 2, upstream.callCount)
			require.Less(t, len(upstream.bodies[1]), len(upstream.bodies[0]))
			require.Equal(t, upstream.reqs[0].URL.String(), upstream.reqs[1].URL.String())
			for _, header := range []string{"Authorization", "X-Api-Key", "session_id", "conversation_id"} {
				require.Equal(t, upstream.reqs[0].Header.Get(header), upstream.reqs[1].Header.Get(header), header)
			}
		})
	}
}

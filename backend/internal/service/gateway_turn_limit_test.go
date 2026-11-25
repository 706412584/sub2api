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

func turnLimitSummaryTestResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"content":[{"type":"text","text":"目标：保留本地定制。已完成：工具曾失败，随后读取成功。待办：继续验证。"}],"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":5}}`))}
}

func turnLimitTestSummarizer(text string, usage OpenAIUsage) func(context.Context, []byte) (string, OpenAIUsage, bool) {
	return func(context.Context, []byte) (string, OpenAIUsage, bool) { return text, usage, true }
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
			upstream := &httpUpstreamSequenceRecorder{responses: []*http.Response{refusal, turnLimitSummaryTestResponse(), {StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(payload))}}}
			cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
			svc := &GatewayService{cfg: cfg, httpUpstream: upstream, responseHeaderFilter: compileResponseHeaderFilter(cfg), rateLimitService: &RateLimitService{}, deferredService: &DeferredService{}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-6", Stream: stream}
			result, err := svc.Forward(context.Background(), c, newAnthropicAPIKeyAccountForTest(), parsed)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 3, upstream.callCount, "首次拒绝 + 模型摘要 + 一次重试")

			// 主请求用量与摘要用量分别核算：摘要只用于恢复，不混入主响应 token。
			require.Equal(t, 7, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
			summaryUsage := turnLimitState(c, time.Now()).usage
			require.Equal(t, 11, summaryUsage.InputTokens)
			require.Equal(t, 5, summaryUsage.OutputTokens)

			require.Less(t, len(upstream.bodies[2]), len(upstream.bodies[0]))
			require.Contains(t, string(upstream.bodies[2]), "工具曾失败，随后读取成功")
			// 摘要请求本身不带业务工具定义，且只含单条引用历史的消息。
			require.False(t, gjson.GetBytes(upstream.bodies[1], "tools").Exists())
			require.Len(t, gjson.GetBytes(upstream.bodies[1], "messages").Array(), 1)
			for _, field := range []string{"model", "system", "tools", "metadata", "stream"} {
				require.Equal(t, gjson.GetBytes(upstream.bodies[0], field).Raw, gjson.GetBytes(upstream.bodies[2], field).Raw, field)
			}
			require.Equal(t, upstream.bodies[2], parsed.Body.Bytes())
			require.NotContains(t, recorder.Body.String(), "session_turn_limit_exceeded")
		})
	}
}

func TestTurnLimitPlanRebuildProtocols(t *testing.T) {
	for _, protocol := range []string{"anthropic", "responses", "chat"} {
		for _, rounds := range []int{99, 100, 101, 220} {
			t.Run(fmt.Sprintf("%s/%d", protocol, rounds), func(t *testing.T) {
				body := turnLimitProtocolBody(t, protocol, rounds)
				plan, ok := planTurnLimitHistory(body, turnLimitHistoryBudget)
				require.True(t, ok)
				retry, ok := plan.rebuild(body, "SUMMARY-MARKER")
				require.True(t, ok)
				require.True(t, utf8.Valid(retry))
				require.Less(t, len(retry), len(body))
				require.Contains(t, string(retry), "SUMMARY-MARKER")

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

func TestTurnLimitRequiresSummarizer(t *testing.T) {
	body := turnLimitMessagesBody(t, 101, false)
	errBody := []byte(`{"error":{"code":"session_turn_limit_exceeded"}}`)
	// 未绑定摘要器时不能静默硬裁剪，必须沿原错误路径返回。
	bare := &turnLimitRetryState{started: time.Now()}
	_, ok := bare.prepare(context.Background(), nil, 400, body, errBody)
	require.False(t, ok)
	require.False(t, bare.tried)

	bound := &turnLimitRetryState{started: time.Now(), summarize: turnLimitTestSummarizer("摘要内容", OpenAIUsage{})}
	retry, ok := bound.prepare(context.Background(), nil, 400, body, errBody)
	require.True(t, ok)
	require.Contains(t, string(retry), "摘要内容")
	require.True(t, bound.tried)
}

func TestTurnLimitShortHistoryStillCompactsTurns(t *testing.T) {
	for _, protocol := range []string{"anthropic", "responses", "chat"} {
		for _, output := range []string{"ok", ""} {
			t.Run(fmt.Sprintf("%s/output=%q", protocol, output), func(t *testing.T) {
				body := turnLimitProtocolBody(t, protocol, 110)
				body = []byte(strings.ReplaceAll(string(body), strings.Repeat("历史工具内容", 800), output))
				require.Less(t, len(body), turnLimitHistoryBudget)
				original := append([]byte(nil), body...)
				state := &turnLimitRetryState{started: time.Now(), summarize: turnLimitTestSummarizer("摘要", OpenAIUsage{})}
				retry, ok := state.prepare(context.Background(), nil, 400, body, []byte(`{"error":{"code":"session_turn_limit_exceeded"}}`))
				require.True(t, ok, "轮数超限不能因历史体积较小而放弃恢复")
				require.Equal(t, original, body, "输入历史不可被就地修改")
				path := "messages"
				if protocol == "responses" {
					path = "input"
				}
				before := gjson.GetBytes(body, path).Array()
				after := gjson.GetBytes(retry, path).Array()
				require.Equal(t, before[0].Raw, after[0].Raw)
				require.Equal(t, before[len(before)-1].Raw, after[len(after)-1].Raw)
				var entries []turnLimitEntry
				turns, visible := 0, 0
				for _, item := range after {
					e, valid := parseTurnLimitEntry(json.RawMessage(item.Raw), protocol == "responses")
					require.True(t, valid)
					entries = append(entries, e)
					visible += e.bytes
					if e.modelTurn {
						turns++
					}
				}
				_, valid := turnLimitCleanBoundaries(entries)
				require.True(t, valid)
				require.Positive(t, turns)
				require.LessOrEqual(t, turns, turnLimitHistoryTurns)
				require.LessOrEqual(t, visible, turnLimitHistoryBudget)
				for key, value := range gjson.ParseBytes(body).Map() {
					if key != path {
						require.Equal(t, value.Raw, gjson.GetBytes(retry, key).Raw, key)
					}
				}
				_, again := state.prepare(context.Background(), nil, 400, retry, []byte(`{"code":"session_turn_limit_exceeded"}`))
				require.False(t, again)
			})
		}
	}
}

func TestTurnLimitShortAssistantTurns(t *testing.T) {
	for _, path := range []string{"messages", "input"} {
		t.Run(path, func(t *testing.T) {
			items := []any{
				map[string]any{"role": "system", "content": "保留系统指令"},
				map[string]any{"role": "developer", "content": "保留开发者指令"},
				map[string]any{"role": "user", "content": "原始任务"},
			}
			for range 110 {
				items = append(items, map[string]any{"role": "assistant", "content": ""})
			}
			items = append(items, map[string]any{"role": "user", "content": "最新问题"})
			body, err := json.Marshal(map[string]any{path: items})
			require.NoError(t, err)
			plan, ok := planTurnLimitHistory(body, turnLimitHistoryBudget)
			require.True(t, ok)
			retry, ok := plan.rebuild(body, "摘要")
			require.True(t, ok)
			after := gjson.GetBytes(retry, path).Array()
			before := gjson.GetBytes(body, path).Array()
			for _, i := range []int{0, 1, 2} {
				require.Equal(t, before[i].Raw, after[i].Raw)
			}
			require.Equal(t, before[len(before)-1].Raw, after[len(after)-1].Raw)
			turns := 0
			for _, item := range after {
				if item.Get("role").String() == "assistant" {
					turns++
				}
			}
			require.Positive(t, turns)
			require.LessOrEqual(t, turns, turnLimitHistoryTurns)
		})
	}
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
			state.started = time.Now().Add(-turnLimitRecoveryTimeout - time.Second)
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
			summarized := 0
			state.summarize = func(context.Context, []byte) (string, OpenAIUsage, bool) {
				summarized++
				return "摘要", OpenAIUsage{}, true
			}
			if tt.setup != nil {
				ctx = tt.setup(ctx, c, state)
			}
			retry, ok := state.prepare(ctx, c, tt.status, body, []byte(tt.errBody))
			require.Equal(t, tt.want, ok)
			if ok {
				require.NotEmpty(t, retry)
				require.Equal(t, 1, summarized)
				require.Contains(t, string(retry), "摘要")
				_, again := state.prepare(ctx, c, tt.status, retry, []byte(tt.errBody))
				require.False(t, again)
				require.Equal(t, 1, summarized, "恢复最多一次")
			} else {
				require.Equal(t, 0, summarized, "未命中恢复条件时不得发起付费摘要")
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
			plan, ok := planTurnLimitHistory(tt.body, tt.budget)
			require.False(t, ok)
			require.Nil(t, plan)
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
			plan, ok := planTurnLimitHistory(body, 4096)
			require.Equal(t, !resultFirst, ok)
			if ok {
				retry, rebuilt := plan.rebuild(body, "摘要")
				require.True(t, rebuilt)
				require.Contains(t, string(retry), call)
				require.Contains(t, string(retry), result)
			}
		})
	}
}

func TestTurnLimitAnchorSurvivesHistory(t *testing.T) {
	messages := []any{map[string]any{"role": "user", "content": "ORIGINAL-TASK " + strings.Repeat("原始任务", 100)}}
	for range 80 {
		messages = append(messages, map[string]any{"role": "user", "content": strings.Repeat("后续问题", 150)})
	}
	body, err := json.Marshal(map[string]any{"messages": messages})
	require.NoError(t, err)
	plan, ok := planTurnLimitHistory(body, 8192)
	require.True(t, ok)
	retry, ok := plan.rebuild(body, "摘要")
	require.True(t, ok)
	require.Contains(t, string(retry), "ORIGINAL-TASK")
}

func TestTurnLimitOversizedAnchorSummarized(t *testing.T) {
	messages := []any{map[string]any{"role": "user", "content": "HUGE-ANCHOR-" + strings.Repeat("旧", 4000)}}
	for range 40 {
		messages = append(messages, map[string]any{"role": "user", "content": strings.Repeat("后", 700) + "LATEST"})
	}
	body, err := json.Marshal(map[string]any{"messages": messages})
	require.NoError(t, err)
	plan, ok := planTurnLimitHistory(body, 8192)
	require.True(t, ok)
	retry, ok := plan.rebuild(body, "摘要")
	require.True(t, ok)
	// 超大锚点不原样保留，交由模型摘要描述；最新用户消息按预算保留。
	require.NotContains(t, string(retry), "HUGE-ANCHOR")
}

func TestTurnLimitResponsesRefusalCountsTowardBudget(t *testing.T) {
	body := []byte(`{"input":[{"role":"user","content":"原始任务"},{"role":"assistant","content":[{"type":"refusal","refusal":"` + strings.Repeat("拒绝", 3000) + `"}]}]}`)
	if plan, ok := planTurnLimitHistory(body, 4096); ok {
		retry, rebuilt := plan.rebuild(body, "摘要")
		require.True(t, rebuilt)
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

func TestTurnLimitMediaDegrade(t *testing.T) {
	media := json.RawMessage(`{"role":"user","content":[{"type":"text","text":"看这张图"},{"type":"image","source":{"type":"base64","data":"` + strings.Repeat("a", 20000) + `"}}]}`)
	degraded, changed := turnLimitDegradeMedia(media, false)
	require.True(t, changed)
	require.Contains(t, string(degraded), "历史媒体因中转会话容量限制已省略")
	require.NotContains(t, string(degraded), strings.Repeat("a", 20000))
	// tool 参数里的同名字段不能被误判为媒体。
	tool := json.RawMessage(`{"role":"assistant","content":[{"type":"tool_use","id":"a","input":{"type":"image","data":"business-value"}}]}`)
	unchanged, changed := turnLimitDegradeMedia(tool, false)
	require.False(t, changed)
	require.Equal(t, tool, unchanged)
}

type turnLimitTestReadCloser struct {
	io.Reader
	closed bool
}

func (r *turnLimitTestReadCloser) Close() error { r.closed = true; return nil }

func TestTurnLimitResponseReplayAndClosure(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		t.Run(fmt.Sprint(oversized), func(t *testing.T) {
			errBody := `{"code":"invalid_request_error"}`
			if oversized {
				errBody += strings.Repeat(" ", 70*1024)
			}
			original := &turnLimitTestReadCloser{Reader: strings.NewReader(errBody)}
			resp := &http.Response{StatusCode: 400, Body: original}
			state := &turnLimitRetryState{started: time.Now(), summarize: turnLimitTestSummarizer("摘要", OpenAIUsage{})}
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
		state := &turnLimitRetryState{started: time.Now(), summarize: turnLimitTestSummarizer("摘要", OpenAIUsage{})}
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
	state := &turnLimitRetryState{started: time.Now(), summarize: turnLimitTestSummarizer("摘要", OpenAIUsage{})}
	_, _, err := state.recoverResponse(context.Background(), nil, &http.Response{StatusCode: 400, Body: original}, turnLimitMessagesBody(t, 101, false), func(retry []byte) (*http.Response, []byte, error) {
		require.True(t, original.closed)
		return nil, retry, sentinel
	})
	require.ErrorIs(t, err, sentinel)
	require.True(t, state.tried)
}

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
				responses := []*http.Response{
					{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"error":{"code":%q,"message":"reached 100 model turns"}}`, refusalCode)))},
				}
				if refusalCode == turnLimitErrorCode {
					responses = append(responses, turnLimitSummaryTestResponse())
				}
				responses = append(responses, &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"session_turn_limit_exceeded","message":"still rejected"}}`))})
				upstream := &httpUpstreamSequenceRecorder{responses: responses}
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
					want = 3
					require.Less(t, len(upstream.bodies[2]), len(upstream.bodies[0]))
				}
				require.Equal(t, want, upstream.callCount)
			})
		}
	}
}

func TestReadTurnLimitSummaryFormats(t *testing.T) {
	sse := func(body string) *http.Response {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	}
	t.Run("anthropic-sse", func(t *testing.T) {
		body := "event: message_start\r\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7}}}\r\n\r\n" +
			"event: content_block_delta\r\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"摘要正文\"}}\r\n\r\n" +
			"event: message_delta\r\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3},\"delta\":{\"stop_reason\":\"end_turn\"}}\r\n\r\n" +
			"event: message_stop\r\ndata: {\"type\":\"message_stop\"}\r\n\r\n"
		text, usage, ok := readTurnLimitSummary(sse(body))
		require.True(t, ok)
		require.Equal(t, "摘要正文", text)
		require.Equal(t, 7, usage.InputTokens)
		require.Equal(t, 3, usage.OutputTokens)
	})
	t.Run("openai-responses-sse", func(t *testing.T) {
		body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"摘要\"}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"正文\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":6,\"output_tokens\":2}}}\n\n"
		text, usage, ok := readTurnLimitSummary(sse(body))
		require.True(t, ok)
		require.Equal(t, "摘要正文", text)
		require.Equal(t, 6, usage.InputTokens)
		require.Equal(t, 2, usage.OutputTokens)
	})
	t.Run("chat-completions-sse", func(t *testing.T) {
		body := "data: {\"choices\":[{\"delta\":{\"content\":\"摘要\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"正文\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":6,\"completion_tokens\":2}}\n\n" +
			"data: [DONE]\n\n"
		text, usage, ok := readTurnLimitSummary(sse(body))
		require.True(t, ok)
		require.Equal(t, "摘要正文", text)
		require.Equal(t, 6, usage.InputTokens)
		require.Equal(t, 2, usage.OutputTokens)
	})
	t.Run("tool-call-is-invalid", func(t *testing.T) {
		body := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"x\"}]},\"finish_reason\":\"tool_calls\"}]}\n\n"
		_, _, ok := readTurnLimitSummary(sse(body))
		require.False(t, ok)
	})
	t.Run("incomplete-stream-is-invalid", func(t *testing.T) {
		body := "data: {\"choices\":[{\"delta\":{\"content\":\"半截\"}}]}\n\n"
		_, _, ok := readTurnLimitSummary(sse(body))
		require.False(t, ok)
	})
}

func TestTurnLimitSuccessfulResponseDoesNotTouchHistory(t *testing.T) {
	body := turnLimitMessagesBody(t, 101, false)
	original := append([]byte(nil), body...)
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("success"))}
	state := &turnLimitRetryState{started: time.Now(), summarize: turnLimitTestSummarizer("摘要", OpenAIUsage{})}
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
				turnLimitSummaryTestResponse(),
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
			require.Equal(t, 3, upstream.callCount, "首次拒绝 + 模型摘要 + 一次重试")
			require.Less(t, len(upstream.bodies[2]), len(upstream.bodies[0]))
			// 摘要请求不含业务工具定义。
			require.False(t, gjson.GetBytes(upstream.bodies[1], "tools").Exists())
			require.False(t, gjson.GetBytes(upstream.bodies[1], "functions").Exists())
			require.Equal(t, upstream.reqs[0].URL.String(), upstream.reqs[2].URL.String())
			for _, header := range []string{"Authorization", "X-Api-Key", "session_id", "conversation_id"} {
				require.Equal(t, upstream.reqs[0].Header.Get(header), upstream.reqs[2].Header.Get(header), header)
			}
		})
	}
}

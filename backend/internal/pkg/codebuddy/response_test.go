package codebuddy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const sseFixture = "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1753600000,\"model\":\"glm-5.2\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"你好\"}}]}\n\n" +
	"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1753600000,\"model\":\"glm-5.2\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"，世界\"}}]}\n\n" +
	"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1753600000,\"model\":\"glm-5.2\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n\n" +
	"data: [DONE]\n\n"

// bufWriter 是 FrameWriter 的最小测试实现（strings.Builder + Flush 空操作）。
type bufWriter struct {
	sb strings.Builder
}

func (w *bufWriter) Write(p []byte) (int, error) { return w.sb.Write(p) }
func (w *bufWriter) Flush()                      {}
func (w *bufWriter) String() string              { return w.sb.String() }

// mustMap / mustSlice / mustFloat 是测试内的类型断言助手。
// .golangci.yml 开启 errcheck 的 check-type-assertions，裸断言会被判为未检查错误；
// 用助手集中处理，既满足 linter 又避免每处重复 ok 判断。
func mustMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("want map[string]any, got %T", v)
	}
	return m
}

func mustSlice(t *testing.T, v any) []any {
	t.Helper()
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("want []any, got %T", v)
	}
	return s
}

func mustFloat(t *testing.T, v any) float64 {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("want float64, got %T", v)
	}
	return f
}

// firstMessage 取 resp.choices[0].message（Aggregate 结果的常见断言入口）。
func firstMessage(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	return mustMap(t, mustMap(t, mustSlice(t, resp["choices"])[0])["message"])
}

// firstChoice 取 resp.choices[0]。
func firstChoice(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	return mustMap(t, mustSlice(t, resp["choices"])[0])
}

func TestAggregate(t *testing.T) {
	resp, err := Aggregate(strings.NewReader(sseFixture))
	if err != nil {
		t.Fatal(err)
	}
	if resp["object"] != "chat.completion" {
		t.Errorf("object=%v want chat.completion", resp["object"])
	}
	if resp["model"] != "glm-5.2" {
		t.Errorf("model=%v", resp["model"])
	}
	choices, ok := resp["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("choices=%v", resp["choices"])
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		t.Fatalf("choice=%v", choices[0])
	}
	msg, ok := choice["message"].(map[string]any)
	if !ok {
		t.Fatalf("message=%v", choice["message"])
	}
	if msg["content"] != "你好，世界" {
		t.Errorf("content=%q", msg["content"])
	}
	if msg["role"] != "assistant" {
		t.Errorf("role=%v", msg["role"])
	}
	if choice["finish_reason"] != "stop" {
		t.Errorf("finish_reason=%v", choice["finish_reason"])
	}
	usage, ok := resp["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage=%v", resp["usage"])
	}
	if total, ok := usage["total_tokens"].(float64); !ok || total != 7 {
		t.Errorf("usage=%v", usage)
	}
}

func TestAggregateSkipsNonDataLines(t *testing.T) {
	raw := ": comment\n\n" + sseFixture
	resp, err := Aggregate(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	msg := firstMessage(t, resp)
	if msg["content"] != "你好，世界" {
		t.Errorf("content=%q", msg["content"])
	}
}

// TestAggregateReasoningContent reasoning_content 必须聚合到 message 上（非流式回译需要）。
func TestAggregateReasoningContent(t *testing.T) {
	raw := "data: {\"id\":\"r1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"思考\"}}]}\n\n" +
		"data: {\"id\":\"r1\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"中\"}}]}\n\n" +
		"data: {\"id\":\"r1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"答\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	resp, err := Aggregate(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	msg := firstMessage(t, resp)
	if msg["reasoning_content"] != "思考中" {
		t.Errorf("reasoning_content=%q want 思考中", msg["reasoning_content"])
	}
	if msg["content"] != "答" {
		t.Errorf("content=%q", msg["content"])
	}
}

func TestAggregateToolCalls(t *testing.T) {
	// 流式 tool_calls：首片带 id/type/name + 空 arguments，后续只带 arguments 片段
	raw := `data: {"id":"x1","model":"deepseek-v4-pro","created":1,"choices":[{"index":0,"delta":{"role":"assistant","content":"","tool_calls":[{"id":"call_a","type":"function","function":{"name":"get_weather","arguments":""},"index":0}]}}],"usage":null}

data: {"id":"x1","choices":[{"index":0,"delta":{"tool_calls":[{"function":{"arguments":"{\"city\":"},"index":0}]}}]}

data: {"id":"x1","choices":[{"index":0,"delta":{"tool_calls":[{"function":{"arguments":"\"北京\"}"},"index":0}]}}]}

data: {"id":"x1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"total_tokens":11}}

data: [DONE]

`
	resp, err := Aggregate(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	choice := firstChoice(t, resp)
	if choice["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason=%v", choice["finish_reason"])
	}
	msg := mustMap(t, choice["message"])
	calls, ok := msg["tool_calls"].([]map[string]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("tool_calls=%#v", msg["tool_calls"])
	}
	if calls[0]["id"] != "call_a" || calls[0]["type"] != "function" {
		t.Errorf("call meta=%v", calls[0])
	}
	fn := mustMap(t, calls[0]["function"])
	if fn["name"] != "get_weather" {
		t.Errorf("fn.name=%v", fn["name"])
	}
	if fn["arguments"] != `{"city":"北京"}` {
		t.Errorf("fn.arguments=%q", fn["arguments"])
	}
}

// TestAggregateToolCallsMultipleIndices 多个 tool_call 按 index 分别合并并按 index 升序输出。
func TestAggregateToolCallsMultipleIndices(t *testing.T) {
	raw := `data: {"id":"x1","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_b","type":"function","function":{"name":"b","arguments":"{}"},"index":1}]}}]}

data: {"id":"x1","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_a","type":"function","function":{"name":"a","arguments":"{}"},"index":0}]}}]}

data: {"id":"x1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	resp, err := Aggregate(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	msg := firstMessage(t, resp)
	calls := msg["tool_calls"].([]map[string]any)
	if len(calls) != 2 {
		t.Fatalf("calls=%d want 2", len(calls))
	}
	if calls[0]["id"] != "call_a" || calls[1]["id"] != "call_b" {
		t.Errorf("调用顺序应为 index 升序，got %v / %v", calls[0]["id"], calls[1]["id"])
	}
}

// TestAggregateMessageFallback 上游把完整消息放在 message（非 delta）时也要取到内容。
func TestAggregateMessageFallback(t *testing.T) {
	raw := "data: {\"id\":\"m1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"message\":{\"role\":\"assistant\",\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	resp, err := Aggregate(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	msg := firstMessage(t, resp)
	if msg["content"] != "hi" {
		t.Errorf("content=%q want hi", msg["content"])
	}
}

// TestAggregateEmptyStreamCases 覆盖空流检测：0 有效事件必须报错、[DONE] 即 break、
// [DONE] 后垃圾不进聚合、正常聚合回归。
func TestAggregateEmptyStreamCases(t *testing.T) {
	valid := "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"glm-5.2\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"}}]}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":7}}\n\n" +
		"data: [DONE]\n\n"

	cases := []struct {
		name        string
		raw         string
		wantErr     bool
		wantContent string
		wantUsage   float64
	}{
		{name: "空流（EOF 即止）", raw: "", wantErr: true},
		{name: "只有注释行和空行加 DONE", raw: ": comment\n\n: another comment\n\ndata: [DONE]\n\n", wantErr: true},
		{name: "只有非法 JSON 帧", raw: "data: {broken\n\ndata: [DONE]\n\n", wantErr: true},
		{
			name:        "DONE 后跟垃圾帧不进聚合",
			raw:         valid[:len(valid)-len("data: [DONE]\n\n")] + "data: [DONE]\n\ndata: {\"junk\":\"should not aggregate\"}\n\n",
			wantContent: "hi",
			wantUsage:   7,
		},
		{name: "正常流回归", raw: valid, wantContent: "hi", wantUsage: 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := Aggregate(strings.NewReader(c.raw))
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (resp=%v)", resp)
				}
				if !errors.Is(err, ErrEmptyStream) {
					t.Errorf("err=%v want ErrEmptyStream", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			msg := firstMessage(t, resp)
			if msg["content"] != c.wantContent {
				t.Errorf("content=%q want %q", msg["content"], c.wantContent)
			}
			u, ok := resp["usage"].(map[string]any)
			if !ok {
				t.Fatal("usage missing")
			}
			if mustFloat(t, u["total_tokens"]) != c.wantUsage {
				t.Errorf("usage=%v want %v", u["total_tokens"], c.wantUsage)
			}
		})
	}
}

func TestNormalizeFrame(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want string // 规范化后 marshal 的期望 JSON（Go map 键按字典序输出）
	}{
		{"空 content/refusal 与空 finish_reason",
			map[string]any{"id": "x", "choices": []any{
				map[string]any{"index": 0, "delta": map[string]any{"content": "", "refusal": ""}, "finish_reason": ""},
			}},
			`{"choices":[{"delta":{},"finish_reason":null,"index":0}],"id":"x","object":"chat.completion.chunk","usage":null}`},
		{"非空 tool_calls 保留",
			map[string]any{"choices": []any{
				map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"id": "c1", "type": "function"}}}},
			}},
			`{"choices":[{"delta":{"tool_calls":[{"id":"c1","type":"function"}]},"finish_reason":null,"index":0}],"id":"chatcmpl-wb2api","object":"chat.completion.chunk","usage":null}`},
		{"空 tool_calls 列表剔除",
			map[string]any{"choices": []any{
				map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{}, "content": "hi"}},
			}},
			`{"choices":[{"delta":{"content":"hi"},"finish_reason":null,"index":0}],"id":"chatcmpl-wb2api","object":"chat.completion.chunk","usage":null}`},
		{"空占位 function_call 剔除",
			map[string]any{"choices": []any{
				map[string]any{"index": 0, "delta": map[string]any{"function_call": map[string]any{"name": "", "arguments": ""}}},
			}},
			`{"choices":[{"delta":{},"finish_reason":null,"index":0}],"id":"chatcmpl-wb2api","object":"chat.completion.chunk","usage":null}`},
		{"顶层未知字段剔除，usage 缺失补 null",
			map[string]any{"id": "x", "object": "chat.completion.chunk", "created": 1, "junk": "noise", "choices": []any{
				map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"},
			}},
			`{"choices":[{"delta":{},"finish_reason":"stop","index":0}],"created":1,"id":"x","object":"chat.completion.chunk","usage":null}`},
		{"reasoning_content 非空保留",
			map[string]any{"id": "x", "choices": []any{
				map[string]any{"index": 0, "delta": map[string]any{"reasoning_content": "想", "content": ""}},
			}},
			`{"choices":[{"delta":{"reasoning_content":"想"},"finish_reason":null,"index":0}],"id":"x","object":"chat.completion.chunk","usage":null}`},
		{"非空 function_call 保留",
			map[string]any{"id": "x", "choices": []any{
				map[string]any{"index": 0, "delta": map[string]any{"function_call": map[string]any{"name": "f"}}},
			}},
			`{"choices":[{"delta":{"function_call":{"name":"f"}},"finish_reason":null,"index":0}],"id":"x","object":"chat.completion.chunk","usage":null}`},
		{"非 map 的 choice 元素跳过",
			map[string]any{"id": "x", "choices": []any{"junk", 42}},
			`{"choices":[],"id":"x","object":"chat.completion.chunk","usage":null}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := json.Marshal(NormalizeFrame(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != c.want {
				t.Errorf("got  %s\nwant %s", raw, c.want)
			}
		})
	}
}

// streamFrames 把原始 SSE 输入经 Stream 处理后解析出所有 JSON 帧及 [DONE] 计数。
func streamFrames(t *testing.T, raw string) (frames []map[string]any, doneCount int) {
	t.Helper()
	w := &bufWriter{}
	if err := Stream(w, strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	for _, ln := range strings.Split(w.String(), "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "data: [DONE]") {
			doneCount++
			continue
		}
		if strings.HasPrefix(ln, "data: ") {
			var obj map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(ln, "data: ")), &obj); err != nil {
				t.Fatalf("bad frame %q: %v", ln, err)
			}
			frames = append(frames, obj)
		}
	}
	return frames, doneCount
}

func TestStreamNormalizesFrames(t *testing.T) {
	// 混合噪声帧：空 content/reasoning/refusal/function_call + 空 tool_calls + 顶层非标字段，
	// 随后非空 content + tool_calls 帧，最后 finish/usage 帧。
	raw := "data: {\"id\":\"x1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\",\"reasoning_content\":\"\",\"refusal\":\"\",\"tool_calls\":[],\"function_call\":{\"name\":\"\",\"arguments\":\"\"}},\"finish_reason\":\"\"}],\"extra_field\":\"junk\"}\n\n" +
		"data: {\"id\":\"x1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\",\"tool_calls\":[{\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"city\\\":\\\"北京\\\"}\"},\"index\":0}]},\"finish_reason\":\"\"}]}\n\n" +
		"data: {\"id\":\"x1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":7}}\n\n" +
		"data: [DONE]\n\n"

	frames, done := streamFrames(t, raw)
	if done != 1 {
		t.Fatalf("done frames=%d want 1", done)
	}
	if len(frames) != 3 {
		t.Fatalf("frames=%d want 3", len(frames))
	}

	f0 := frames[0]
	if _, ok := f0["extra_field"]; ok {
		t.Error("top-level extra_field should be dropped")
	}
	if f0["usage"] != nil {
		t.Errorf("usage should be null when absent, got %v", f0["usage"])
	}
	ch0 := firstChoice(t, f0)
	if ch0["finish_reason"] != nil {
		t.Errorf("frame1 finish_reason=%v want null", ch0["finish_reason"])
	}
	d := mustMap(t, ch0["delta"])
	if len(d) != 1 || d["role"] != "assistant" {
		t.Errorf("frame1 delta should only keep role, got %#v", d)
	}
	for _, noise := range []string{"content", "reasoning_content", "refusal", "tool_calls", "function_call"} {
		if _, ok := d[noise]; ok {
			t.Errorf("frame1 delta should drop %q, got %#v", noise, d)
		}
	}

	f1 := frames[1]
	ch1 := firstChoice(t, f1)
	d1 := mustMap(t, ch1["delta"])
	if d1["content"] != "hello" {
		t.Errorf("frame2 content=%v", d1["content"])
	}
	tcs, ok := d1["tool_calls"].([]any)
	if !ok || len(tcs) != 1 {
		t.Fatalf("frame2 tool_calls=%#v", d1["tool_calls"])
	}
	if ch1["finish_reason"] != nil {
		t.Errorf("frame2 finish_reason=%v want null (input empty string)", ch1["finish_reason"])
	}

	f2 := frames[2]
	ch2 := firstChoice(t, f2)
	if ch2["finish_reason"] != "stop" {
		t.Errorf("frame3 finish_reason=%v want stop", ch2["finish_reason"])
	}
	if mustFloat(t, mustMap(t, f2["usage"])["total_tokens"]) != 7 {
		t.Errorf("frame3 usage=%v", f2["usage"])
	}
}

func TestStreamDoneFallback(t *testing.T) {
	// 上游流在无 [DONE] 时 EOF，Stream 必须兜底写一个 [DONE]
	w := &bufWriter{}
	if err := Stream(w, strings.NewReader("data: {\"id\":\"x1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimRight(w.String(), "\n"), "data: [DONE]") {
		t.Errorf("missing [DONE] fallback: %q", w.String())
	}

	// 已有 [DONE] 时只写一次，不重复
	w2 := &bufWriter{}
	if err := Stream(w2, strings.NewReader("data: {\"id\":\"x1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n")); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(w2.String(), "data: [DONE]"); n != 1 {
		t.Errorf("[DONE] count=%d want 1: %q", n, w2.String())
	}
}

func TestStreamPassthrough(t *testing.T) {
	w := &bufWriter{}
	if err := Stream(w, strings.NewReader(sseFixture)); err != nil {
		t.Fatal(err)
	}
	body := w.String()
	if !strings.Contains(body, "你好") || !strings.Contains(body, "data: [DONE]") {
		t.Errorf("body missing chunks: %q", body)
	}
	for _, ln := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if ln != "" && !strings.HasPrefix(ln, "data: ") {
			t.Errorf("bad line: %q", ln)
		}
	}
}

// TestStreamEmptyFramesCase 覆盖流式空流检测：0 有效帧时写 error 帧（error 字段存活）,
// 恰好一个 [DONE]，并返回 ErrEmptyStream。
func TestStreamEmptyFramesCase(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"空流", ""},
		{"只有注释行", ": comment\n\n"},
		{"只有 DONE", "data: [DONE]\n\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := &bufWriter{}
			err := Stream(w, strings.NewReader(c.raw))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, ErrEmptyStream) {
				t.Errorf("err=%v want ErrEmptyStream", err)
			}
			body := w.String()
			if n := strings.Count(body, "data: [DONE]"); n != 1 {
				t.Errorf("[DONE] count=%d want 1: %q", n, body)
			}
			// error 帧必须原样保留 error 字段（未被 NormalizeFrame 白名单剥掉）
			found := false
			for _, ln := range strings.Split(body, "\n") {
				ln = strings.TrimSpace(ln)
				if !strings.HasPrefix(ln, "data: ") {
					continue
				}
				payload := strings.TrimPrefix(ln, "data: ")
				if payload == "[DONE]" {
					continue
				}
				var e map[string]any
				if json.Unmarshal([]byte(payload), &e) == nil {
					if em, ok := e["error"].(map[string]any); ok && em["message"] == "empty upstream stream" && em["type"] == "upstream_error" {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("error frame absent or error field stripped: %q", body)
			}
		})
	}
}

// TestStreamGarbageAfterDone 校验 DONE 之后的垃圾帧不出现在响应里。
func TestStreamGarbageAfterDone(t *testing.T) {
	raw := "data: {\"id\":\"x1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n" +
		"data: [DONE]\n\n" +
		"data: {\"should\":\"not appear\"}\n\n"
	w := &bufWriter{}
	if err := Stream(w, strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	body := w.String()
	if strings.Contains(body, "should") {
		t.Errorf("garbage after DONE leaked into response: %q", body)
	}
	if n := strings.Count(body, "data: [DONE]"); n != 1 {
		t.Errorf("[DONE] count=%d want 1: %q", n, body)
	}
	if !strings.Contains(body, "hello") {
		t.Errorf("valid frame missing: %q", body)
	}
}

// TestStreamNormalPassthroughRegression 校验正常透传回归：帧被 normalize 后透传、
// 末尾恰好一个 [DONE]、无 error 帧；上游漏发 DONE 时自动补。
func TestStreamNormalPassthroughRegression(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"带 DONE 的正常流", sseFixture},
		{"漏发 DONE 自动补", "data: {\"id\":\"x1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := &bufWriter{}
			if err := Stream(w, strings.NewReader(c.raw)); err != nil {
				t.Fatal(err)
			}
			body := w.String()
			if strings.Contains(body, `"error"`) {
				t.Errorf("unexpected error frame: %q", body)
			}
			if n := strings.Count(body, "data: [DONE]"); n != 1 {
				t.Errorf("[DONE] count=%d want 1: %q", n, body)
			}
			if !strings.Contains(body, `"object":"chat.completion.chunk"`) {
				t.Errorf("frame not normalized: %q", body)
			}
		})
	}
}

// TestStreamCommentLinesPassthrough 注释/非 data 行原样透传（含心跳）。
func TestStreamCommentLinesPassthrough(t *testing.T) {
	raw := ": ping\n\ndata: {\"id\":\"x1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"
	w := &bufWriter{}
	if err := Stream(w, strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	body := w.String()
	if !strings.Contains(body, ": ping") {
		t.Errorf("comment line should be passed through: %q", body)
	}
}

// TestStreamCRLFAndChunkedLines 分片到达（无完整行）与 CRLF 行尾都要正确解析。
func TestStreamCRLFAndChunkedLines(t *testing.T) {
	// CRLF 行尾
	crlf := "data: {\"id\":\"x1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\r\n\r\ndata: [DONE]\r\n\r\n"
	frames, done := streamFrames(t, crlf)
	if len(frames) != 1 || done != 1 {
		t.Fatalf("CRLF: frames=%d done=%d want 1/1", len(frames), done)
	}
	// 末尾无换行（EOF 截断的半行仍应被处理）
	noNewline := "data: {\"id\":\"x1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}"
	w := &bufWriter{}
	if err := Stream(w, strings.NewReader(noNewline)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "hi") {
		t.Errorf("truncated last line should still be emitted: %q", w.String())
	}
}

func TestSetSSEHeaders(t *testing.T) {
	h := map[string][]string{}
	SetSSEHeaders(h)
	for _, k := range []string{"Content-Type", "Cache-Control", "Connection", "X-Accel-Buffering"} {
		if len(h[k]) == 0 {
			t.Errorf("header %s not set", k)
		}
	}
	if h["Content-Type"][0] != "text/event-stream" {
		t.Errorf("Content-Type=%v", h["Content-Type"])
	}
}

func TestMergeToolCallDelta(t *testing.T) {
	merged := map[string]any{}
	mergeToolCallDelta(merged, map[string]any{
		"id": "c1", "type": "function",
		"function": map[string]any{"name": "f", "arguments": ""},
	})
	if merged["id"] != "c1" || merged["type"] != "function" {
		t.Fatalf("meta not merged: %v", merged)
	}
	fn := mustMap(t, merged["function"])
	if fn["name"] != "f" {
		t.Fatalf("name=%v", fn["name"])
	}
	if _, ok := fn["arguments"]; ok {
		t.Errorf("empty arguments should not be written: %v", fn)
	}
	mergeToolCallDelta(merged, map[string]any{"function": map[string]any{"arguments": "{}"}})
	if fn["arguments"] != "{}" {
		t.Errorf("arguments=%v want {}", fn["arguments"])
	}
	mergeToolCallDelta(merged, map[string]any{"function": map[string]any{"arguments": "x"}})
	if fn["arguments"] != "{}x" {
		t.Errorf("arguments=%v want {}x", fn["arguments"])
	}
	// 无 function 的片段不 panic 也不改写
	mergeToolCallDelta(merged, map[string]any{"id": "c2"})
	if merged["id"] != "c2" {
		t.Errorf("id=%v want c2", merged["id"])
	}
}

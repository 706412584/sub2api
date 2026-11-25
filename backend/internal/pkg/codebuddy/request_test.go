package codebuddy

import (
	"encoding/json"
	"testing"
)

func TestPrepareBodyForcesStream(t *testing.T) {
	out := PrepareBody([]byte(`{"model":"glm-5.2","messages":[]}`), nil)
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["stream"] != true {
		t.Errorf("stream=%v want true", m["stream"])
	}
}

func TestPrepareBodyInvalidJSON(t *testing.T) {
	in := []byte(`{broken`)
	out := PrepareBody(in, nil)
	if string(out) != string(in) {
		t.Errorf("invalid json should pass through unchanged, got %s", out)
	}
}

func TestPrepareBodyEmptyInput(t *testing.T) {
	if out := PrepareBody(nil, nil); len(out) != 0 {
		t.Errorf("nil input should return nil, got %q", out)
	}
}

// TestPrepareBodyNormalizeRoles 验证出站请求体把 developer 角色归一为 system。
// 上游 role 白名单不含 developer（OpenAI 新规范的 system 别名），
// 命中即 HTTP 400 code=11128。
//
// 注意：本用例同时穿过 ensureSystemPrompt（首条非 system 会补一条默认 system），
// 因此首条不是 system 的输入，期望值里会多出一位前导 "system"；
// 补全行为本身由 TestEnsureSystemPrompt 专门覆盖。
func TestPrepareBodyNormalizeRoles(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantRoles []string // 与输出 messages 逐条对应的期望 role；len 即消息数
	}{
		{"developer 改写为 system（已成 system，不再补）",
			`{"messages":[{"role":"developer","content":"x"}]}`, []string{"system"}},
		{"Developer 首字母大写改写",
			`{"messages":[{"role":"Developer","content":"x"}]}`, []string{"system"}},
		{"DEVELOPER 全大写改写",
			`{"messages":[{"role":"DEVELOPER","content":"x"}]}`, []string{"system"}},
		{"前后空白 TrimSpace 后改写",
			`{"messages":[{"role":" developer ","content":"x"}]}`, []string{"system"}},
		{"system 原样保留（不重复补）",
			`{"messages":[{"role":"system","content":"x"}]}`, []string{"system"}},
		{"user 本身原样保留，仅在其前补一条 system",
			`{"messages":[{"role":"user","content":"x"}]}`, []string{"system", "user"}},
		{"assistant 本身原样保留，仅在其前补一条 system",
			`{"messages":[{"role":"assistant","content":"x"}]}`, []string{"system", "assistant"}},
		{"tool 本身原样保留（不因未知而改写）",
			`{"messages":[{"role":"tool","content":"x"}]}`, []string{"system", "tool"}},
		{"messages 缺失不补不 panic 且其余字段不变",
			`{"model":"glm-5.2"}`, []string{}},
		{"messages 为空数组不补（空列表本就非法，交上游报错）",
			`{"messages":[]}`, []string{}},
		{"混合消息：首条 developer 转 system 后不再补，其余原样",
			`{"messages":[{"role":"developer","content":"a"},{"role":"user","content":"b"},{"role":"developer","content":"c"}]}`,
			[]string{"system", "user", "system"}},
		{"非对象消息元素跳过、其余正常处理",
			`{"messages":["str",{"role":"developer","content":"x"},42]}`, []string{"system"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := PrepareBody([]byte(c.body), nil)
			var obj map[string]any
			if err := json.Unmarshal(out, &obj); err != nil {
				t.Fatalf("unmarshal: %v (out=%s)", err, out)
			}

			var got []string
			if msgs, ok := obj["messages"].([]any); ok {
				for _, m := range msgs {
					msg, ok := m.(map[string]any)
					if !ok {
						continue
					}
					if role, ok := msg["role"].(string); ok {
						got = append(got, role)
					}
				}
			}

			if len(got) != len(c.wantRoles) {
				t.Fatalf("role 数量不符: got %v (%d) want %v (%d)", got, len(got), c.wantRoles, len(c.wantRoles))
			}
			for i := range got {
				if got[i] != c.wantRoles[i] {
					t.Errorf("role[%d] = %q want %q", i, got[i], c.wantRoles[i])
				}
			}
		})
	}

	// messages 缺失时，其余字段必须原样保留（除强制 stream）。
	t.Run("messages 缺失时其余字段不变", func(t *testing.T) {
		out := PrepareBody([]byte(`{"model":"glm-5.2","temperature":0.7}`), nil)
		var obj map[string]any
		if err := json.Unmarshal(out, &obj); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if obj["model"] != "glm-5.2" || obj["temperature"] != 0.7 {
			t.Errorf("其余字段被改动: %v", obj)
		}
	})
}

// TestEnsureSystemPrompt 系统提示词补全：仅在「缺失」或「内容为空」时补默认值，
// 有内容则完全不碰（不能覆盖调用方自己的提示词）。
func TestEnsureSystemPrompt(t *testing.T) {
	firstOf := func(t *testing.T, body string) (role string, content any, count int) {
		t.Helper()
		out := PrepareBody([]byte(body), nil)
		var obj map[string]any
		if err := json.Unmarshal(out, &obj); err != nil {
			t.Fatalf("unmarshal: %v (out=%s)", err, out)
		}
		msgs, _ := obj["messages"].([]any)
		if len(msgs) == 0 {
			return "", nil, 0
		}
		m, _ := msgs[0].(map[string]any)
		role, _ = m["role"].(string)
		return role, m["content"], len(msgs)
	}

	t.Run("缺失 system → 在最前补一条，原有消息不动", func(t *testing.T) {
		role, content, n := firstOf(t, `{"messages":[{"role":"user","content":"hi"}]}`)
		if role != "system" || content != defaultSystemPrompt {
			t.Fatalf("首条=%q/%v want system/%q", role, content, defaultSystemPrompt)
		}
		if n != 2 {
			t.Errorf("消息数=%d want 2", n)
		}
	})

	t.Run("system 内容为空串 → 就地填入默认值，不新增消息", func(t *testing.T) {
		role, content, n := firstOf(t, `{"messages":[{"role":"system","content":""},{"role":"user","content":"hi"}]}`)
		if role != "system" || content != defaultSystemPrompt {
			t.Fatalf("首条=%q/%v want system/%q", role, content, defaultSystemPrompt)
		}
		if n != 2 {
			t.Errorf("消息数=%d want 2（应替换内容而非新增）", n)
		}
	})

	t.Run("system 内容为纯空白 → 视为空并填入", func(t *testing.T) {
		_, content, _ := firstOf(t, `{"messages":[{"role":"system","content":"   \n\t "},{"role":"user","content":"hi"}]}`)
		if content != defaultSystemPrompt {
			t.Errorf("content=%v want %q", content, defaultSystemPrompt)
		}
	})

	t.Run("system 缺 content 字段 → 视为空并填入", func(t *testing.T) {
		_, content, _ := firstOf(t, `{"messages":[{"role":"system"},{"role":"user","content":"hi"}]}`)
		if content != defaultSystemPrompt {
			t.Errorf("content=%v want %q", content, defaultSystemPrompt)
		}
	})

	t.Run("system content 为空数组 → 视为空并填入", func(t *testing.T) {
		_, content, _ := firstOf(t, `{"messages":[{"role":"system","content":[]},{"role":"user","content":"hi"}]}`)
		if content != defaultSystemPrompt {
			t.Errorf("content=%v want %q", content, defaultSystemPrompt)
		}
	})

	// 关键：有内容时必须原样保留，不能被网关覆盖。
	t.Run("system 有内容 → 完全不动", func(t *testing.T) {
		const mine = "你是一只猫，只用喵回答"
		role, content, n := firstOf(t, `{"messages":[{"role":"system","content":"`+mine+`"},{"role":"user","content":"hi"}]}`)
		if role != "system" || content != mine {
			t.Fatalf("首条=%q/%v want system/%q（不得覆盖调用方提示词）", role, content, mine)
		}
		if n != 2 {
			t.Errorf("消息数=%d want 2（不得新增）", n)
		}
	})

	t.Run("system 内容为数组且非空 → 不动", func(t *testing.T) {
		_, content, _ := firstOf(t, `{"messages":[{"role":"system","content":[{"type":"text","text":"hi"}]},{"role":"user","content":"x"}]}`)
		if _, isArr := content.([]any); !isArr {
			t.Errorf("content=%v want 保持数组形态", content)
		}
	})

	t.Run("首条消息非对象 → 不 panic 不注入", func(t *testing.T) {
		out := PrepareBody([]byte(`{"messages":["str","another"]}`), nil)
		var obj map[string]any
		if err := json.Unmarshal(out, &obj); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		msgs, _ := obj["messages"].([]any)
		if len(msgs) != 2 {
			t.Errorf("消息数=%d want 2（首条非对象时不动）", len(msgs))
		}
	})
}

func TestPrepareBodyToolChoice(t *testing.T) {
	t.Run("function 对象 → 名字字符串，tools 保留", func(t *testing.T) {
		out := PrepareBody([]byte(`{"tool_choice":{"type":"function","function":{"name":"get_weather"}},"tools":[{"type":"function"}]}`), nil)
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatal(err)
		}
		if m["tool_choice"] != "get_weather" {
			t.Errorf("tool_choice=%v want get_weather", m["tool_choice"])
		}
		if _, ok := m["tools"]; !ok {
			t.Error("tools should be kept for function choice")
		}
	})

	t.Run("none → 删 tool_choice 与 tools/functions", func(t *testing.T) {
		for _, in := range []string{
			`{"tool_choice":"none","tools":[{}],"functions":[{}]}`,
			`{"tool_choice":{"type":"none"},"tools":[{}]}`,
			`{"tool_choice":{"type":"none"},"tools":[{}],"functions":[{}]}`,
		} {
			out := PrepareBody([]byte(in), nil)
			var m map[string]any
			if err := json.Unmarshal(out, &m); err != nil {
				t.Fatal(err)
			}
			if _, ok := m["tool_choice"]; ok {
				t.Errorf("%s: tool_choice should be deleted", in)
			}
			if _, ok := m["tools"]; ok {
				t.Errorf("%s: tools should be deleted", in)
			}
			if _, ok := m["functions"]; ok {
				t.Errorf("%s: functions should be deleted", in)
			}
		}
	})

	t.Run("auto/required → 字符串", func(t *testing.T) {
		for _, tc := range []struct{ in, want string }{
			{`{"tool_choice":{"type":"auto"}}`, "auto"},
			{`{"tool_choice":{"type":"required"}}`, "required"},
			{`{"tool_choice":{"type":"AUTO"}}`, "auto"},
		} {
			out := PrepareBody([]byte(tc.in), nil)
			var m map[string]any
			if err := json.Unmarshal(out, &m); err != nil {
				t.Fatal(err)
			}
			if m["tool_choice"] != tc.want {
				t.Errorf("%s: tool_choice=%v want %q", tc.in, m["tool_choice"], tc.want)
			}
		}
	})

	t.Run("function 无名字 → 回落 auto", func(t *testing.T) {
		out := PrepareBody([]byte(`{"tool_choice":{"type":"function"}}`), nil)
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatal(err)
		}
		if m["tool_choice"] != "auto" {
			t.Errorf("tool_choice=%v want auto", m["tool_choice"])
		}
	})

	t.Run("未知对象形态 → 删除", func(t *testing.T) {
		out := PrepareBody([]byte(`{"tool_choice":{"type":"weird"},"tools":[{}]}`), nil)
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatal(err)
		}
		if _, ok := m["tool_choice"]; ok {
			t.Errorf("tool_choice should be deleted, got %v", m["tool_choice"])
		}
		if _, ok := m["tools"]; !ok {
			t.Error("tools should be kept for unknown tool_choice object")
		}
	})

	t.Run("非 none 字符串 → 原样保留", func(t *testing.T) {
		out := PrepareBody([]byte(`{"tool_choice":"get_weather","tools":[{}]}`), nil)
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatal(err)
		}
		if m["tool_choice"] != "get_weather" {
			t.Errorf("tool_choice=%v", m["tool_choice"])
		}
	})

	t.Run("缺字段 → 不动", func(t *testing.T) {
		out := PrepareBody([]byte(`{"model":"glm-5.2"}`), nil)
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatal(err)
		}
		if _, ok := m["tool_choice"]; ok {
			t.Error("tool_choice should not be injected")
		}
	})
}

func TestPrepareBodyReasoningEffort(t *testing.T) {
	efforts := map[string][]string{
		"glm-5.2":      {"off", "low", "high"},
		"glm-5.2-mini": {"low", "medium"},
		"glm-5.2-max":  {"high", "xhigh"},
	}
	cases := []struct {
		name    string
		body    string
		efforts map[string][]string
		wantKey string // 输出应带有的 effort 字段名；空表示该字段应不存在
		wantVal string // 期望值
	}{
		{"降级到 ≤请求档位的最高支持档",
			`{"model":"glm-5.2-mini","reasoning_effort":"high"}`, efforts, "reasoning_effort", "medium"},
		{"支持档全高于请求档 → 取最低支持档",
			`{"model":"glm-5.2-max","reasoning_effort":"low"}`, efforts, "reasoning_effort", "high"},
		{"支持的档位原样透传",
			`{"model":"glm-5.2","reasoning_effort":"low"}`, efforts, "reasoning_effort", "low"},
		{"camelCase 字段名同样降级且保留键名",
			`{"model":"glm-5.2-mini","reasoningEffort":"high"}`, efforts, "reasoningEffort", "medium"},
		{"未知模型透传",
			`{"model":"unknown","reasoning_effort":"max"}`, efforts, "reasoning_effort", "max"},
		{"未知档位值透传",
			`{"model":"glm-5.2","reasoning_effort":"ultra"}`, efforts, "reasoning_effort", "ultra"},
		{"空能力表透传",
			`{"model":"glm-5.2","reasoning_effort":"max"}`, map[string][]string{}, "reasoning_effort", "max"},
		{"未携带字段不动",
			`{"model":"glm-5.2-mini","messages":[]}`, efforts, "", ""},
		{"nil 能力表透传",
			`{"model":"glm-5.2","reasoning_effort":"max"}`, nil, "reasoning_effort", "max"},
		{"大小写与空白归一后匹配",
			`{"model":"glm-5.2-mini","reasoning_effort":" HIGH "}`, efforts, "reasoning_effort", "medium"},
		{"请求档位高于所有支持档 → 取最高支持档",
			`{"model":"glm-5.2","reasoning_effort":"max"}`, efforts, "reasoning_effort", "high"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := PrepareBody([]byte(c.body), c.efforts)
			var m map[string]any
			if err := json.Unmarshal(out, &m); err != nil {
				t.Fatalf("unmarshal: %v (body=%s)", err, out)
			}
			if c.wantKey == "" {
				if _, ok := m["reasoning_effort"]; ok {
					t.Errorf("reasoning_effort should be absent, got %v", m["reasoning_effort"])
				}
				if _, ok := m["reasoningEffort"]; ok {
					t.Errorf("reasoningEffort should be absent, got %v", m["reasoningEffort"])
				}
				return
			}
			got, ok := m[c.wantKey].(string)
			if !ok || got != c.wantVal {
				t.Errorf("%s: got %v (%T) want %q", c.wantKey, m[c.wantKey], m[c.wantKey], c.wantVal)
			}
		})
	}
}

// TestPrepareBodyReasoningEffortPassthroughWhenModelEmpty 空模型名不触发降级。
func TestPrepareBodyReasoningEffortPassthroughWhenModelEmpty(t *testing.T) {
	efforts := map[string][]string{"glm-5.2": {"low"}}
	out := PrepareBody([]byte(`{"reasoning_effort":"max"}`), efforts)
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["reasoning_effort"] != "max" {
		t.Errorf("reasoning_effort=%v want max", m["reasoning_effort"])
	}
}

func TestSystemContentEmpty(t *testing.T) {
	cases := []struct {
		in   any
		want bool
	}{
		{nil, true},
		{"", true},
		{"   \n\t", true},
		{"hi", false},
		{[]any{}, true},
		{[]any{map[string]any{"type": "text"}}, false},
		{42, false},
	}
	for _, c := range cases {
		if got := systemContentEmpty(c.in); got != c.want {
			t.Errorf("systemContentEmpty(%#v)=%v want %v", c.in, got, c.want)
		}
	}
}

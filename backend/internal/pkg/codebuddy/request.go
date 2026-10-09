// request.go 改写发往上游的 chat 请求体：
//  1. 强制 stream:true（上游拒绝非流式）
//  2. tool_choice 归一化（上游该字段是 string，对象形式会 400 code=11101）
//  3. role 归一化（developer → system，上游 role 白名单不含 developer，code=11128）
//  4. 系统提示词补全（首条非 system 或其内容为空时补默认值，上游要求首条必须是 system）
//  5. reasoning_effort 按模型 supportedEfforts 降级
package codebuddy

import (
	"encoding/json"
	"strings"
)

// defaultSystemPrompt 请求的系统提示词为空时代为注入的内容。
const defaultSystemPrompt = "You are a helpful assistant."

// PrepareBody 对请求体做上游所需的全部改写。
// efforts 为模型 → 支持的 reasoning 档位（nil 表示未知，不降级）。
// 解析失败或序列化失败时原样返回入参。
func PrepareBody(src []byte, efforts map[string][]string) []byte {
	if len(src) == 0 {
		return src
	}
	var obj map[string]any
	if err := json.Unmarshal(src, &obj); err != nil {
		return src
	}
	obj["stream"] = true
	normalizeToolChoice(obj)
	normalizeRoles(obj)
	ensureSystemPrompt(obj)
	normalizeReasoningEffort(obj, efforts)
	out, err := json.Marshal(obj)
	if err != nil {
		return src
	}
	return out
}

// ensureSystemPrompt 保证首条消息是内容非空的 system；仅在其为空时才补，有内容则完全不动。
//
// 背景：上游校验首条必须是 system，否则 HTTP 400 code=11128
// （"first message is not system prompt"）。部分 OpenAI 兼容客户端（如 Cherry Studio）
// 的连通性检查与默认对话不带 system，或把 system 配成空串，请求会被上游直接拒绝；
// 协议兼容由网关承担，调用方无需改客户端。
//
// 三种处置：
//   - 首条非 system → 在最前插入一条默认 system
//   - 首条是 system 但内容为空 → 就地填入默认内容（不新增第二条：语义上就是补全该条）
//   - 首条是 system 且内容非空 → 不动，尊重调用方自己的提示词
//
// 必须在 normalizeRoles 之后调用：developer 会被归一为 system，
// 若先注入再归一，首条 developer 会被补成两条 system。
// messages 缺失或为空时不注入——空列表本就非法，交由上游报错，避免掩盖真实问题。
func ensureSystemPrompt(obj map[string]any) {
	msgs, ok := obj["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return
	}
	first, ok := msgs[0].(map[string]any)
	if !ok {
		return
	}
	role, _ := first["role"].(string)
	if !strings.EqualFold(strings.TrimSpace(role), "system") {
		sys := map[string]any{"role": "system", "content": defaultSystemPrompt}
		obj["messages"] = append([]any{sys}, msgs...)
		return
	}
	if systemContentEmpty(first["content"]) {
		first["content"] = defaultSystemPrompt
	}
}

// systemContentEmpty 报告 system 消息的 content 是否为空。
// 兼容两种形态：字符串（缺失/空串/纯空白）与数组（空数组）。
func systemContentEmpty(v any) bool {
	switch c := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(c) == ""
	case []any:
		return len(c) == 0
	default:
		return false
	}
}

// effortRank 档位从低到高。
var effortRank = map[string]int{"off": 0, "minimal": 1, "low": 2, "medium": 3, "high": 4, "xhigh": 5, "max": 6}

// normalizeReasoningEffort 按模型 supportedEfforts 降级 reasoning_effort（snake/camel 双字段兼容）。
//   - 请求档位模型支持 → 原样透传
//   - 请求档位不支持 → 改为 ≤请求档位的最高支持档（降级）
//   - 支持档全部高于请求档 → 取最低支持档（偏离最小）
//   - 未知模型/未知档位/未携带字段/模型未缓存 → 一律透传
func normalizeReasoningEffort(obj map[string]any, efforts map[string][]string) {
	if len(efforts) == 0 {
		return
	}
	model, _ := obj["model"].(string)
	if model == "" {
		return
	}
	supported, ok := efforts[model]
	if !ok || len(supported) == 0 {
		return
	}
	key := ""
	if _, present := obj["reasoning_effort"]; present {
		key = "reasoning_effort"
	} else if _, present := obj["reasoningEffort"]; present {
		key = "reasoningEffort"
	} else {
		return
	}
	reqStr, ok := obj[key].(string)
	if !ok {
		return
	}
	reqStr = strings.TrimSpace(strings.ToLower(reqStr))
	reqIdx, known := effortRank[reqStr]
	if !known {
		return
	}
	// 在 ≤请求档位的支持档里选最高档；命中且与请求不同才改写。
	best, bestIdx := "", -1
	for _, s := range supported {
		idx, k := effortRank[strings.TrimSpace(strings.ToLower(s))]
		if k && idx <= reqIdx && idx > bestIdx {
			best, bestIdx = s, idx
		}
	}
	if best != "" {
		if !strings.EqualFold(best, reqStr) {
			obj[key] = best
		}
		return
	}
	// 支持档全部高于请求档：取最低支持档。
	lowest, lowestIdx := "", 1<<30
	for _, s := range supported {
		idx, k := effortRank[strings.TrimSpace(strings.ToLower(s))]
		if k && idx < lowestIdx {
			lowest, lowestIdx = s, idx
		}
	}
	if lowest != "" {
		obj[key] = lowest
	}
}

// normalizeRoles 把 messages 里的 developer 角色归一为 system。
//
// 背景：上游对 messages 的 role 字段做白名单校验，developer 不在白名单内，
// 命中即 HTTP 400 code=11128。developer 是 OpenAI 新规范里 system 的别名
// （Codex / Cursor 等新客户端用它承载 system 级指令），改写为 system 不丢语义。
//
// 只认 developer 这一个值：其余 role（system/user/assistant/tool/任意未知值）一律原样保留，
// 不合并、不重排、不删除任何消息。
func normalizeRoles(obj map[string]any) {
	msgs, ok := obj["messages"].([]any)
	if !ok {
		return
	}
	for _, m := range msgs {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, ok := msg["role"].(string)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(role), "developer") {
			msg["role"] = "system"
		}
	}
}

// normalizeToolChoice 按上游 Go struct（string 类型）改写 OpenAI tool_choice。
//   - "none"            → 删 tool_choice + 删 tools/functions
//   - {"type":"none"}   → 同上
//   - {"type":"auto"/"required"} → 字符串 "auto"/"required"
//   - {"type":"function","function":{"name":"x"}} → 字符串 "x"
//   - 其他对象/非标量 → 删 tool_choice
func normalizeToolChoice(obj map[string]any) {
	suppress := func() {
		delete(obj, "tools")
		delete(obj, "functions")
	}
	tc, present := obj["tool_choice"]
	if !present {
		return
	}
	switch v := tc.(type) {
	case string:
		if strings.EqualFold(strings.TrimSpace(v), "none") {
			delete(obj, "tool_choice")
			suppress()
		}
	case map[string]any:
		typ, _ := v["type"].(string)
		typ = strings.ToLower(strings.TrimSpace(typ))
		switch typ {
		case "none":
			delete(obj, "tool_choice")
			suppress()
		case "auto", "required":
			obj["tool_choice"] = typ
		case "function":
			name := ""
			if fn, ok := v["function"].(map[string]any); ok {
				name, _ = fn["name"].(string)
			}
			if name == "" {
				name, _ = v["name"].(string)
			}
			if name = strings.TrimSpace(name); name != "" {
				obj["tool_choice"] = name
			} else {
				obj["tool_choice"] = "auto"
			}
		default:
			delete(obj, "tool_choice")
		}
	default:
		delete(obj, "tool_choice")
	}
}

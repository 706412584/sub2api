package codebuddy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ErrKind 上游错误分类，驱动账号池的冷却状态机。
type ErrKind int

const (
	ErrNone        ErrKind = iota // 成功
	ErrHardCredit                 // 余额不足（402 或 body 关键词）→ 长冷却
	ErrSoftRate                   // 限流 → 短冷却
	ErrSessionDead                // 会话失效（12153 / Offline user session not found）→ 需人工重登
	ErrNotFound                   // 404 上游偶发 → 短冷却，不累计错误计数（防雪崩）
	ErrServer                     // 5xx 上游故障
	ErrClient                     // 其他 4xx / 业务错误
)

func (k ErrKind) String() string {
	switch k {
	case ErrHardCredit:
		return "hard_credit"
	case ErrSoftRate:
		return "soft_rate"
	case ErrSessionDead:
		return "session_dead"
	case ErrNotFound:
		return "not_found"
	case ErrServer:
		return "server"
	case ErrClient:
		return "client"
	default:
		return "none"
	}
}

// Error 带分类的上游错误。
type Error struct {
	Kind   ErrKind
	Status int
	Code   int    // 上游业务 code（信封内的 code 字段），0 表示无
	Msg    string // 已截断的响应片段
}

func (e *Error) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("codebuddy upstream %s (http %d, code %d): %s", e.Kind, e.Status, e.Code, e.Msg)
	}
	return fmt.Sprintf("codebuddy upstream %s (http %d): %s", e.Kind, e.Status, e.Msg)
}

// hardMarkers 余额不足关键词（小写比较 + 中文原文比较双通道）。
//
// 单复数必须分别列出：Contains 是子串匹配，词中间插了 's' 就对不上。
// "insufficient credit" / "no credit" / "out of credit" 恰好是复数形式的前缀，
// 能直接命中；唯独 "credit exhausted" 的 's' 在词中间，漏了它会让上游的
// "Credits exhausted"（HTTP 429 + code=14018）落进下面的 429 兜底被判成 soft_rate：
// 账号只软冷却十分钟就回到池中反复失败，而不会停到次日 04:00 等签到恢复。
var hardMarkers = []string{
	"insufficient credit", "no credit", "credit exhausted", "credits exhausted",
	"out of credit",
	"quota exceeded", "quota exhaust", "payment required", "credit not enough",
	"not enough credit",
	"积分不足", "额度不足", "余额不足", "积分用完", "额度用尽", "没有积分",
}

// softRateMarkers 限流/节流关键词（小写比较 + 中文原文比较双通道）。
// 上游在状态码非 429 时也会返回限流语义（如 200 + code 11140
// "The model provider is rate-limiting requests."、400 + "rate limit"）。
//
// 词表按子串匹配，宁缺毋滥：只收录明确指向「请求速率/模型用量被节流」的措辞。
// 连字符形式（rate-limiting / rate-limited）需单列——Contains 不跨 '-'。
// "too many" 会命中 "too many tokens" 这类客户端参数错误，代价是该号被软冷却
// 一个短周期后自愈，远小于漏判限流导致反复选中同一号的代价。
var softRateMarkers = []string{
	"rate limit", // rate limit / rate limits / rate limiting
	"rate-limiting",
	"rate-limited",
	"too many requests",
	"too many",
	"usage limit", // usage limit reached / model usage limit exceeded（用量节流，非计费余额）
	"请求过于频繁", "限流",
}

var sessionDeadMarkers = []string{"Offline user session not found", "12153"}

// CodeModelUnavailable 上游对不存在/不可用模型返回的业务码。
// 语义是「本请求的模型在这个账号上不可用」，属于请求级瞬时失败：
// 应换账号重试，但不得标记账号凭据失效。
const CodeModelUnavailable = 11102

// Classify 按 HTTP 状态码 + body 判定错误类别。
//
// 判定顺序自「严」到「宽」，每层的先后都有语义依据：
//  1. 402 / hardMarkers —— 计费额度耗尽，最严、最不可自愈，必须最先判。
//     "quota exceeded" 语义跨计费/限流两界，历史归 hard_credit，保持不变。
//  2. sessionDeadMarkers —— 需要人工重登的终态。若 401 body 同时含 "12153" 与
//     "rate limit"（如网关错误页混排），归 session_dead：短冷却救不活失效 session，
//     误判为限流会让该死号留在池中反复被选中；且此层 marker 是精确词（12153 等），
//     比限流层的大范围子串更具体，具体优先于宽泛。
//  3. softRateMarkers —— 非 429 状态码携带限流文案。位于此处可覆盖
//     200/400/403/5xx 各状态码；429 且 body 含文案时在此短路，结果同为 soft_rate。
//  4. status==429 —— body 无文案时的兜底识别。
//  5. 404 / 5xx / 其他 4xx —— 与限流无关的常规分类。
func Classify(status int, body string) ErrKind {
	if status == http.StatusPaymentRequired {
		return ErrHardCredit
	}
	lower := strings.ToLower(body)
	for _, m := range hardMarkers {
		if strings.Contains(lower, strings.ToLower(m)) || strings.Contains(body, m) {
			return ErrHardCredit
		}
	}
	for _, m := range sessionDeadMarkers {
		if strings.Contains(body, m) {
			return ErrSessionDead
		}
	}
	for _, m := range softRateMarkers {
		if strings.Contains(lower, strings.ToLower(m)) || strings.Contains(body, m) {
			return ErrSoftRate
		}
	}
	if status == http.StatusTooManyRequests {
		return ErrSoftRate
	}
	if status == http.StatusNotFound {
		return ErrNotFound
	}
	if status >= 500 {
		return ErrServer
	}
	if status >= 400 {
		return ErrClient
	}
	return ErrNone
}

// envelope 上游统一信封。
type envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// ParseEnvelope 解析信封并返回 data；HTTP 非 2xx 或业务 code != 0 时返回 *Error。
func ParseEnvelope(status int, raw []byte) (json.RawMessage, error) {
	if status >= 400 {
		return nil, &Error{Kind: Classify(status, string(raw)), Status: status, Msg: Truncate(string(raw), 200)}
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("codebuddy: parse envelope failed: %w (body: %s)", err, Truncate(string(raw), 120))
	}
	if env.Code != 0 {
		kind := Classify(status, env.Msg)
		if kind == ErrNone {
			kind = ErrClient
		}
		return nil, &Error{
			Kind:   kind,
			Status: status,
			Code:   env.Code,
			Msg:    Truncate(env.Msg, 160),
		}
	}
	return env.Data, nil
}

// Truncate 截断字符串到 n 字节（先 TrimSpace）。
func Truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

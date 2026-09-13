package codebuddy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   ErrKind
	}{
		{402, ``, ErrHardCredit},
		{400, `{"code":1,"msg":"余额不足"}`, ErrHardCredit},
		{403, `insufficient credits`, ErrHardCredit},
		{200, `{"code":10001,"msg":"积分不足，请充值"}`, ErrHardCredit},
		{400, `{"code":1,"msg":"额度用尽"}`, ErrHardCredit},
		{429, ``, ErrSoftRate},
		// 限流文案：状态码不是 429 时也必须识别为软限流，
		// 否则账号不会被冷却，下次请求仍会被选中。
		{200, `{"code":11140,"msg":"The model provider is rate-limiting requests. Please wait a moment and try again."}`, ErrSoftRate},
		{400, `rate limit`, ErrSoftRate},
		{403, `usage limit reached`, ErrSoftRate},
		// "model usage limit exceeded" 不是余额语义（无 credit/quota/积分/额度 等计费词），
		// 属于模型侧用量节流 → 短冷却（误判为硬冷却会把有余量的号停到次日）。
		{200, `{"code":1,"msg":"model usage limit exceeded"}`, ErrSoftRate},
		{200, `{"code":1,"msg":"too many requests"}`, ErrSoftRate},
		{500, `rate-limited upstream`, ErrSoftRate}, // 限流文案优先于 5xx 分类
		// 反向锚定：不得回归。
		{400, `Illegal API invocation from an unapproved channel`, ErrClient},
		{200, `quota exceeded`, ErrHardCredit},
		// session 死亡优先于限流文案（401+12153 需人工重登，短冷却无意义）。
		{401, `{"code":12153,"msg":"Offline user session not found, rate limit"}`, ErrSessionDead},
		{401, `Offline user session not found`, ErrSessionDead},
		{401, `{"code":12153,"msg":"Offline user session not found"}`, ErrSessionDead},
		{401, `{"code":9999,"msg":"bad token"}`, ErrClient},
		{404, `not found`, ErrNotFound},
		{500, `boom`, ErrServer},
		{503, `unavailable`, ErrServer},
		{200, ``, ErrNone},
		{204, ``, ErrNone},
	}
	for _, c := range cases {
		if got := Classify(c.status, c.body); got != c.want {
			t.Errorf("Classify(%d,%q)=%v want %v", c.status, c.body, got, c.want)
		}
	}
}

func TestErrKindString(t *testing.T) {
	cases := map[ErrKind]string{
		ErrNone:        "none",
		ErrHardCredit:  "hard_credit",
		ErrSoftRate:    "soft_rate",
		ErrSessionDead: "session_dead",
		ErrNotFound:    "not_found",
		ErrServer:      "server",
		ErrClient:      "client",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("%d.String()=%q want %q", k, got, want)
		}
	}
}

func TestParseEnvelopeSuccess(t *testing.T) {
	data, err := ParseEnvelope(200, []byte(`{"code":0,"msg":"ok","data":{"accessToken":"t1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.AccessToken != "t1" {
		t.Errorf("accessToken=%q want t1", out.AccessToken)
	}
}

func TestParseEnvelopeErrors(t *testing.T) {
	t.Run("HTTP 非 2xx 直接分类", func(t *testing.T) {
		_, err := ParseEnvelope(402, []byte(`{"code":1,"msg":"x"}`))
		var ue *Error
		if !errors.As(err, &ue) {
			t.Fatalf("err=%v want *Error", err)
		}
		if ue.Kind != ErrHardCredit || ue.Status != 402 {
			t.Errorf("kind=%v status=%d", ue.Kind, ue.Status)
		}
	})

	t.Run("业务 code 非 0 分类并保留 code", func(t *testing.T) {
		_, err := ParseEnvelope(200, []byte(`{"code":11102,"msg":"service info not found"}`))
		var ue *Error
		if !errors.As(err, &ue) {
			t.Fatalf("err=%v want *Error", err)
		}
		if ue.Code != CodeModelUnavailable {
			t.Errorf("code=%d want %d", ue.Code, CodeModelUnavailable)
		}
		if ue.Kind != ErrClient {
			t.Errorf("kind=%v want client（无关键词命中时回落 client）", ue.Kind)
		}
		if !strings.Contains(ue.Error(), "11102") {
			t.Errorf("Error()=%q 应含 code", ue.Error())
		}
	})

	t.Run("业务 code 非 0 且 msg 命中限流", func(t *testing.T) {
		_, err := ParseEnvelope(200, []byte(`{"code":11140,"msg":"rate limit exceeded"}`))
		var ue *Error
		if !errors.As(err, &ue) {
			t.Fatalf("err=%v want *Error", err)
		}
		if ue.Kind != ErrSoftRate {
			t.Errorf("kind=%v want soft_rate", ue.Kind)
		}
	})

	t.Run("非法 JSON", func(t *testing.T) {
		if _, err := ParseEnvelope(200, []byte(`{broken`)); err == nil {
			t.Fatal("expected error for invalid json")
		}
	})
}

func TestErrorFormat(t *testing.T) {
	e := &Error{Kind: ErrSoftRate, Status: 429, Code: 11140, Msg: "slow down"}
	if !strings.Contains(e.Error(), "soft_rate") || !strings.Contains(e.Error(), "11140") {
		t.Errorf("Error()=%q", e.Error())
	}
	e2 := &Error{Kind: ErrServer, Status: 500, Msg: "boom"}
	if !strings.Contains(e2.Error(), "http 500") {
		t.Errorf("Error()=%q", e2.Error())
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"  hello  ", 10, "hello"},
		{"hello world", 5, "hello"},
		{"hi", 5, "hi"},
		{"", 5, ""},
	}
	for _, c := range cases {
		if got := Truncate(c.in, c.n); got != c.want {
			t.Errorf("Truncate(%q,%d)=%q want %q", c.in, c.n, got, c.want)
		}
	}
}

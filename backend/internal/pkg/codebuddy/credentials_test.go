package codebuddy

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFromCredentialsMapBothKeyStyles(t *testing.T) {
	// snake_case（面板手填）
	snake := FromCredentialsMap(map[string]any{
		"access_token":  "at",
		"refresh_token": "rt",
		"expires_at":    float64(1753600000),
		"domain":        "d.com",
		"uid":           "u",
		"enterprise_id": "e",
		"nickname":      "n",
	})
	// camelCase（导入 auths/*.json）
	camel := FromCredentialsMap(map[string]any{
		"accessToken":  "at",
		"refreshToken": "rt",
		"expiresAt":    "1753600000",
		"domain":       "d.com",
		"uid":          "u",
		"enterpriseId": "e",
		"nickname":     "n",
	})
	if snake != camel {
		t.Errorf("两种键名解析结果应一致:\nsnake=%+v\ncamel=%+v", snake, camel)
	}
	if snake.AccessToken != "at" || snake.RefreshToken != "rt" || snake.ExpiresAt != 1753600000 {
		t.Errorf("解析错误: %+v", snake)
	}

	// nil map 不 panic
	if got := FromCredentialsMap(nil); got != (Credentials{}) {
		t.Errorf("nil map should give zero value, got %+v", got)
	}
}

func TestCredentialInt64Forms(t *testing.T) {
	for _, v := range []any{float64(100), int64(100), 100, "100", " 100 "} {
		got := credentialInt64(map[string]any{"expires_at": v}, "expires_at")
		if got != 100 {
			t.Errorf("credentialInt64(%#v)=%d want 100", v, got)
		}
	}
	// 不可解析值返回 0
	for _, v := range []any{"abc", nil, []any{}, map[string]any{}} {
		if got := credentialInt64(map[string]any{"expires_at": v}, "expires_at"); got != 0 {
			t.Errorf("credentialInt64(%#v)=%d want 0", v, got)
		}
	}
}

func TestCredentialStringTrimsAndSkipsEmpty(t *testing.T) {
	m := map[string]any{"a": "  ", "b": " value "}
	if got := credentialString(m, "a", "b"); got != "value" {
		t.Errorf("got %q want value（空白应跳过取下一个键）", got)
	}
	if got := credentialString(m, "missing"); got != "" {
		t.Errorf("got %q want empty", got)
	}
}

func TestCredentialsValidateAndRefresh(t *testing.T) {
	if err := (Credentials{}).Validate(); err == nil {
		t.Error("empty credentials should fail validation")
	}
	if err := (Credentials{AccessToken: "  "}).Validate(); err == nil {
		t.Error("whitespace-only access token should fail validation")
	}
	if err := (Credentials{AccessToken: "t"}).Validate(); err != nil {
		t.Errorf("valid credentials rejected: %v", err)
	}

	if (Credentials{AccessToken: "t"}).ShouldRefresh() {
		t.Error("no refresh token → ShouldRefresh must be false")
	}
	if !(Credentials{AccessToken: "t", RefreshToken: "r"}).ShouldRefresh() {
		t.Error("with refresh token → ShouldRefresh must be true")
	}
}

func TestNeedsRefresh(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		name      string
		expiresAt int64
		within    time.Duration
		want      bool
	}{
		{"无过期时间 → 需要刷新", 0, time.Hour, true},
		{"已过期 → 需要刷新", now - 60, time.Hour, true},
		{"在窗口内 → 需要刷新", now + 60, time.Hour, true},
		{"窗口外 → 不需要", now + 7200, time.Hour, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := (Credentials{AccessToken: "t", ExpiresAt: c.expiresAt}).NeedsRefresh(c.within)
			if got != c.want {
				t.Errorf("NeedsRefresh=%v want %v", got, c.want)
			}
		})
	}
	if !(Credentials{}).ExpiresAtTime().IsZero() {
		t.Error("no expiry should give zero time")
	}
	if got := (Credentials{ExpiresAt: 100}).ExpiresAtTime().Unix(); got != 100 {
		t.Errorf("ExpiresAtTime=%d want 100", got)
	}
}

func TestParseAuthFileNested(t *testing.T) {
	raw := `{"auth":{"accessToken":"at","refreshToken":"rt","expiresAt":123,"domain":"d"},
	          "account":{"uid":"u","enterpriseId":"e","nickname":"n"}}`
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	c, err := ParseAuthFile(m)
	if err != nil {
		t.Fatal(err)
	}
	want := Credentials{AccessToken: "at", RefreshToken: "rt", ExpiresAt: 123, Domain: "d", UID: "u", EnterpriseID: "e", Nickname: "n"}
	if c != want {
		t.Errorf("got %+v\nwant %+v", c, want)
	}
}

func TestParseAuthFileFlat(t *testing.T) {
	raw := `{"accessToken":"at","uid":"u","nickname":"n"}`
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	c, err := ParseAuthFile(m)
	if err != nil {
		t.Fatal(err)
	}
	if c.AccessToken != "at" || c.UID != "u" || c.Nickname != "n" {
		t.Errorf("got %+v", c)
	}
}

func TestParseAuthFileErrors(t *testing.T) {
	if _, err := ParseAuthFile(nil); err == nil {
		t.Error("nil should error")
	}
	if _, err := ParseAuthFile(map[string]any{}); err == nil {
		t.Error("missing accessToken should error")
	}
	// 嵌套形但 auth 内无 token
	if _, err := ParseAuthFile(map[string]any{"auth": map[string]any{"refreshToken": "rt"}}); err == nil {
		t.Error("nested without accessToken should error")
	}
}

func TestToCredentialsMapRoundTrip(t *testing.T) {
	c := Credentials{AccessToken: "at", RefreshToken: "rt", ExpiresAt: 123, Domain: "d", UID: "u", EnterpriseID: "e", Nickname: "n"}
	m := c.ToCredentialsMap()
	// 必须能被 FromCredentialsMap 还原（键名一致）
	if got := FromCredentialsMap(m); got != c {
		t.Errorf("round trip 失败:\ngot  %+v\nwant %+v", got, c)
	}
	// 空字段不写键，避免前端显示空凭据
	minimal := Credentials{AccessToken: "at"}.ToCredentialsMap()
	if len(minimal) != 1 {
		t.Errorf("minimal map should only carry access_token, got %v", minimal)
	}
}

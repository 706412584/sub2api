// Package codebuddy 封装腾讯 CodeBuddy 上游（chat / billing / report / 插件鉴权）
// 的协议细节：区域端点、请求头、请求体改写、SSE 帧重建、错误分类、模型发现与区域归属。
//
// 与 kiro 包约定一致：本包只构造 *http.Request 与解析响应字节，
// 不做网络执行——执行统一交给 service 层的 HTTPUpstream（代理 / TLS 指纹 / 并发槽）。
package codebuddy

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Region 上游区域。两区的端点、模型阵容与可用活动均不同。
type Region string

const (
	// RegionCN 国内版（copilot.tencent.com / codebuddy.cn）。
	RegionCN Region = "cn"
	// RegionGlobal 国外版（workbuddy.ai）。
	RegionGlobal Region = "global"
)

// 账号类型常量。区域由账号类型显式决定，不从 domain 隐式推导——
// 建号时用户就明确选区域，且类型可驱动端点、模型表与养号任务资格。
const (
	AccountTypeCN     = "codebuddy_cn"
	AccountTypeGlobal = "codebuddy_global"
)

// IsAccountType 报告 accountType 是否为 CodeBuddy 的两个账号类型之一。
func IsAccountType(accountType string) bool {
	switch accountType {
	case AccountTypeCN, AccountTypeGlobal:
		return true
	default:
		return false
	}
}

// RegionForAccountType 由账号类型推导区域；未知类型回落 CN（向后兼容）。
func RegionForAccountType(accountType string) Region {
	if accountType == AccountTypeGlobal {
		return RegionGlobal
	}
	return RegionCN
}

// Credentials 是归一化后的 CodeBuddy 凭证（对应 Account.Credentials 的子集）。
type Credentials struct {
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    int64  `json:"expiresAt,omitempty"`
	Domain       string `json:"domain,omitempty"`
	UID          string `json:"uid,omitempty"`
	EnterpriseID string `json:"enterpriseId,omitempty"`
	Nickname     string `json:"nickname,omitempty"`
}

// Validate 校验最小可用凭证集。
func (c Credentials) Validate() error {
	if strings.TrimSpace(c.AccessToken) == "" {
		return errors.New("codebuddy: access token is required")
	}
	return nil
}

// ShouldRefresh 报告该凭证是否具备刷新条件。
func (c Credentials) ShouldRefresh() bool {
	return strings.TrimSpace(c.RefreshToken) != ""
}

// NeedsRefresh 报告 token 是否将在 within 内过期（无过期时间视为需要刷新）。
func (c Credentials) NeedsRefresh(within time.Duration) bool {
	if c.ExpiresAt <= 0 {
		return true
	}
	return time.Now().Add(within).Unix() >= c.ExpiresAt
}

// ExpiresAtTime 返回过期时刻；无过期时间返回零值。
func (c Credentials) ExpiresAtTime() time.Time {
	if c.ExpiresAt <= 0 {
		return time.Time{}
	}
	return time.Unix(c.ExpiresAt, 0)
}

// credentialString 从 credentials map 读字符串字段，兼容 camelCase 与 snake_case
// 两种键名（导入的 auths/*.json 用 camelCase，面板手填用 snake_case）。
func credentialString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// credentialInt64 从 credentials map 读整数（JSON 数字反序列化为 float64）。
func credentialInt64(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch n := v.(type) {
			case float64:
				return int64(n)
			case int64:
				return n
			case int:
				return int64(n)
			case string:
				var out int64
				if _, err := fmt.Sscanf(strings.TrimSpace(n), "%d", &out); err == nil {
					return out
				}
			}
		}
	}
	return 0
}

// FromCredentialsMap 把 Account.Credentials 解析为 Credentials。
func FromCredentialsMap(m map[string]any) Credentials {
	if m == nil {
		return Credentials{}
	}
	return Credentials{
		AccessToken:  credentialString(m, "access_token", "accessToken"),
		RefreshToken: credentialString(m, "refresh_token", "refreshToken"),
		ExpiresAt:    credentialInt64(m, "expires_at", "expiresAt"),
		Domain:       credentialString(m, "domain"),
		UID:          credentialString(m, "uid", "userId", "user_id"),
		EnterpriseID: credentialString(m, "enterprise_id", "enterpriseId"),
		Nickname:     credentialString(m, "nickname"),
	}
}

// ParseAuthFile 解析 workbuddy2api 的 auths/*.json（嵌套形或扁平形）。
// 嵌套形：{"auth":{"accessToken","refreshToken","expiresAt","domain"},
//
//	"account":{"uid","enterpriseId","nickname"}}
//
// 扁平形：{"accessToken":...,"uid":...}
func ParseAuthFile(m map[string]any) (Credentials, error) {
	if m == nil {
		return Credentials{}, errors.New("codebuddy: empty auth file")
	}
	if nested, ok := m["auth"].(map[string]any); ok {
		acct, _ := m["account"].(map[string]any)
		merged := make(map[string]any, len(nested)+len(acct))
		for k, v := range nested {
			merged[k] = v
		}
		for k, v := range acct {
			merged[k] = v
		}
		c := FromCredentialsMap(merged)
		if c.AccessToken == "" {
			return Credentials{}, errors.New("codebuddy: missing accessToken in auth file")
		}
		return c, nil
	}
	c := FromCredentialsMap(m)
	if c.AccessToken == "" {
		return Credentials{}, errors.New("codebuddy: missing accessToken in auth file")
	}
	return c, nil
}

// ToCredentialsMap 把 Credentials 展开为 Account.Credentials 的子键（snake_case）。
func (c Credentials) ToCredentialsMap() map[string]any {
	out := map[string]any{
		"access_token": c.AccessToken,
	}
	if c.RefreshToken != "" {
		out["refresh_token"] = c.RefreshToken
	}
	if c.ExpiresAt > 0 {
		out["expires_at"] = c.ExpiresAt
	}
	if c.Domain != "" {
		out["domain"] = c.Domain
	}
	if c.UID != "" {
		out["uid"] = c.UID
	}
	if c.EnterpriseID != "" {
		out["enterprise_id"] = c.EnterpriseID
	}
	if c.Nickname != "" {
		out["nickname"] = c.Nickname
	}
	return out
}

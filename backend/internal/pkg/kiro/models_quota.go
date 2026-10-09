package kiro

import "strings"

type ListAvailableModelsResponse struct {
	Models []UpstreamModel `json:"models"`
}
type UpstreamModel struct {
	ModelID     string       `json:"modelId"`
	ModelName   string       `json:"modelName,omitempty"`
	Description string       `json:"description,omitempty"`
	TokenLimits *TokenLimits `json:"tokenLimits,omitempty"`
}
type TokenLimits struct {
	MaxInputTokens *int64 `json:"maxInputTokens,omitempty"`
}

type UsageLimitsResponse struct {
	NextDateReset        *float64              `json:"nextDateReset,omitempty"`
	SubscriptionInfo     *SubscriptionInfo     `json:"subscriptionInfo,omitempty"`
	UsageBreakdownList   []UsageBreakdown      `json:"usageBreakdownList"`
	OverageConfiguration *OverageConfiguration `json:"overageConfiguration,omitempty"`
	UserInfo             *UserInfo             `json:"userInfo,omitempty"`
}
type UserInfo struct {
	Email string `json:"email,omitempty"`
}
type SubscriptionInfo struct {
	SubscriptionTitle string `json:"subscriptionTitle,omitempty"`
	OverageCapability string `json:"overageCapability,omitempty"`
}
type OverageConfiguration struct {
	OverageEnabled *bool  `json:"overageEnabled,omitempty"`
	OverageStatus  string `json:"overageStatus,omitempty"`
}
type UsageBreakdown struct {
	CurrentUsage              int64          `json:"currentUsage"`
	CurrentUsageWithPrecision float64        `json:"currentUsageWithPrecision"`
	Bonuses                   []Bonus        `json:"bonuses"`
	FreeTrialInfo             *FreeTrialInfo `json:"freeTrialInfo,omitempty"`
	NextDateReset             *float64       `json:"nextDateReset,omitempty"`
	UsageLimit                int64          `json:"usageLimit"`
	UsageLimitWithPrecision   float64        `json:"usageLimitWithPrecision"`
}
type Bonus struct {
	CurrentUsage float64 `json:"currentUsage"`
	UsageLimit   float64 `json:"usageLimit"`
	Status       string  `json:"status,omitempty"`
}
type FreeTrialInfo struct {
	CurrentUsage              int64    `json:"currentUsage"`
	CurrentUsageWithPrecision float64  `json:"currentUsageWithPrecision"`
	FreeTrialExpiry           *float64 `json:"freeTrialExpiry,omitempty"`
	FreeTrialStatus           string   `json:"freeTrialStatus,omitempty"`
	UsageLimit                int64    `json:"usageLimit"`
	UsageLimitWithPrecision   float64  `json:"usageLimitWithPrecision"`
}

func (r UsageLimitsResponse) SubscriptionTitle() string {
	if r.SubscriptionInfo == nil {
		return ""
	}
	return r.SubscriptionInfo.SubscriptionTitle
}
func (r UsageLimitsResponse) Email() string {
	if r.UserInfo == nil {
		return ""
	}
	return r.UserInfo.Email
}
func (r UsageLimitsResponse) OverageEnabled() (bool, bool) {
	if r.OverageConfiguration == nil {
		return false, false
	}
	if r.OverageConfiguration.OverageEnabled != nil {
		return *r.OverageConfiguration.OverageEnabled, true
	}
	if r.OverageConfiguration.OverageStatus == "" {
		return false, false
	}
	return strings.EqualFold(r.OverageConfiguration.OverageStatus, "ENABLED"), true
}
func (r UsageLimitsResponse) OverageCapable() (bool, bool) {
	if r.SubscriptionInfo == nil {
		return false, false
	}
	switch strings.ToUpper(strings.TrimSpace(r.SubscriptionInfo.OverageCapability)) {
	case "OVERAGE_CAPABLE":
		return true, true
	case "NOT_OVERAGE_CAPABLE", "NOT_AVAILABLE":
		return false, true
	default:
		return false, false
	}
}
func (r UsageLimitsResponse) UsageLimit() float64 {
	if len(r.UsageBreakdownList) == 0 {
		return 0
	}
	b := r.UsageBreakdownList[0]
	total := b.UsageLimitWithPrecision
	if b.FreeTrialInfo != nil && b.FreeTrialInfo.FreeTrialStatus == "ACTIVE" {
		total += b.FreeTrialInfo.UsageLimitWithPrecision
	}
	for _, bonus := range b.Bonuses {
		if bonus.Status == "ACTIVE" {
			total += bonus.UsageLimit
		}
	}
	return total
}
func (r UsageLimitsResponse) CurrentUsage() float64 {
	if len(r.UsageBreakdownList) == 0 {
		return 0
	}
	b := r.UsageBreakdownList[0]
	total := b.CurrentUsageWithPrecision
	if b.FreeTrialInfo != nil && b.FreeTrialInfo.FreeTrialStatus == "ACTIVE" {
		total += b.FreeTrialInfo.CurrentUsageWithPrecision
	}
	for _, bonus := range b.Bonuses {
		if bonus.Status == "ACTIVE" {
			total += bonus.CurrentUsage
		}
	}
	return total
}

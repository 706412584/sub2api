package admin

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// GetAdminAPIKey 获取管理员 API Key 状态
// GET /api/v1/admin/settings/admin-api-key
func (h *SettingHandler) GetAdminAPIKey(c *gin.Context) {
	maskedKey, exists, err := h.settingService.GetAdminAPIKeyStatus(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{
		"exists":     exists,
		"masked_key": maskedKey,
	})
}

// RegenerateAdminAPIKey 生成/重新生成管理员 API Key
// POST /api/v1/admin/settings/admin-api-key/regenerate
func (h *SettingHandler) RegenerateAdminAPIKey(c *gin.Context) {
	key, err := h.settingService.GenerateAdminAPIKey(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{
		"key": key, // 完整 key 只在生成时返回一次
	})
}

// DeleteAdminAPIKey 删除管理员 API Key
// DELETE /api/v1/admin/settings/admin-api-key
func (h *SettingHandler) DeleteAdminAPIKey(c *gin.Context) {
	if err := h.settingService.DeleteAdminAPIKey(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "Admin API key deleted"})
}

// GetOverloadCooldownSettings 获取529过载冷却配置
// GET /api/v1/admin/settings/overload-cooldown
func (h *SettingHandler) GetOverloadCooldownSettings(c *gin.Context) {
	settings, err := h.settingService.GetOverloadCooldownSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.OverloadCooldownSettings{
		Enabled:         settings.Enabled,
		CooldownMinutes: settings.CooldownMinutes,
	})
}

// UpdateOverloadCooldownSettingsRequest 更新529过载冷却配置请求
type UpdateOverloadCooldownSettingsRequest struct {
	Enabled         bool `json:"enabled"`
	CooldownMinutes int  `json:"cooldown_minutes"`
}

// UpdateOverloadCooldownSettings 更新529过载冷却配置
// PUT /api/v1/admin/settings/overload-cooldown
func (h *SettingHandler) UpdateOverloadCooldownSettings(c *gin.Context) {
	var req UpdateOverloadCooldownSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	settings := &service.OverloadCooldownSettings{
		Enabled:         req.Enabled,
		CooldownMinutes: req.CooldownMinutes,
	}

	if err := h.settingService.SetOverloadCooldownSettings(c.Request.Context(), settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updatedSettings, err := h.settingService.GetOverloadCooldownSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.OverloadCooldownSettings{
		Enabled:         updatedSettings.Enabled,
		CooldownMinutes: updatedSettings.CooldownMinutes,
	})
}

// GetRateLimit429CooldownSettings 获取429默认回避配置
// GET /api/v1/admin/settings/rate-limit-429-cooldown
func (h *SettingHandler) GetRateLimit429CooldownSettings(c *gin.Context) {
	settings, err := h.settingService.GetRateLimit429CooldownSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.RateLimit429CooldownSettings{
		Enabled:         settings.Enabled,
		CooldownSeconds: settings.CooldownSeconds,
	})
}

// UpdateRateLimit429CooldownSettingsRequest 更新429默认回避配置请求
type UpdateRateLimit429CooldownSettingsRequest struct {
	Enabled         bool `json:"enabled"`
	CooldownSeconds int  `json:"cooldown_seconds"`
}

// UpdateRateLimit429CooldownSettings 更新429默认回避配置
// PUT /api/v1/admin/settings/rate-limit-429-cooldown
func (h *SettingHandler) UpdateRateLimit429CooldownSettings(c *gin.Context) {
	var req UpdateRateLimit429CooldownSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	settings := &service.RateLimit429CooldownSettings{
		Enabled:         req.Enabled,
		CooldownSeconds: req.CooldownSeconds,
	}

	if err := h.settingService.SetRateLimit429CooldownSettings(c.Request.Context(), settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updatedSettings, err := h.settingService.GetRateLimit429CooldownSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.RateLimit429CooldownSettings{
		Enabled:         updatedSettings.Enabled,
		CooldownSeconds: updatedSettings.CooldownSeconds,
	})
}

// GetOpenAIImagesOAuthUnavailableCooldownSettings 获取 OpenAI OAuth 图片工具不可用冷却配置
// GET /api/v1/admin/settings/openai-images-oauth-unavailable-cooldown
func (h *SettingHandler) GetOpenAIImagesOAuthUnavailableCooldownSettings(c *gin.Context) {
	settings, err := h.settingService.GetOpenAIImagesOAuthUnavailableCooldownSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.OpenAIImagesOAuthUnavailableCooldownSettings{CooldownMinutes: settings.CooldownMinutes})
}

type UpdateOpenAIImagesOAuthUnavailableCooldownSettingsRequest struct {
	CooldownMinutes int `json:"cooldown_minutes"`
}

func (h *SettingHandler) UpdateOpenAIImagesOAuthUnavailableCooldownSettings(c *gin.Context) {
	var req UpdateOpenAIImagesOAuthUnavailableCooldownSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings := &service.OpenAIImagesOAuthUnavailableCooldownSettings{CooldownMinutes: req.CooldownMinutes}
	if err := h.settingService.SetOpenAIImagesOAuthUnavailableCooldownSettings(c.Request.Context(), settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, dto.OpenAIImagesOAuthUnavailableCooldownSettings{CooldownMinutes: settings.CooldownMinutes})
}

// GetOpenAIGrok429ExhaustionSettings 获取 GPT/Grok 429 立即限流配置
// GET /api/v1/admin/settings/openai-grok-429-exhaustion
func (h *SettingHandler) GetOpenAIGrok429ExhaustionSettings(c *gin.Context) {
	settings, err := h.settingService.GetOpenAIGrok429ExhaustionSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.OpenAIGrok429ExhaustionSettings{
		Enabled:                  settings.Enabled,
		FreeFullDurationHours:    settings.FreeFullDurationHours,
		FreeFullThresholdPercent: settings.FreeFullThresholdPercent,
		NoResetDurationMinutes:   settings.NoResetDurationMinutes,
	})
}

// UpdateOpenAIGrok429ExhaustionSettingsRequest 更新 GPT/Grok 429 立即限流配置请求
type UpdateOpenAIGrok429ExhaustionSettingsRequest struct {
	Enabled                  bool    `json:"enabled"`
	FreeFullDurationHours    int     `json:"free_full_duration_hours"`
	FreeFullThresholdPercent float64 `json:"free_full_threshold_percent"`
	NoResetDurationMinutes   int     `json:"no_reset_duration_minutes"`
}

// UpdateOpenAIGrok429ExhaustionSettings 更新 GPT/Grok 429 立即限流配置
// PUT /api/v1/admin/settings/openai-grok-429-exhaustion
func (h *SettingHandler) UpdateOpenAIGrok429ExhaustionSettings(c *gin.Context) {
	var req UpdateOpenAIGrok429ExhaustionSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings := &service.OpenAIGrok429ExhaustionSettings{
		Enabled:                  req.Enabled,
		FreeFullDurationHours:    req.FreeFullDurationHours,
		FreeFullThresholdPercent: req.FreeFullThresholdPercent,
		NoResetDurationMinutes:   req.NoResetDurationMinutes,
	}
	if err := h.settingService.SetOpenAIGrok429ExhaustionSettings(c.Request.Context(), settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	updated, err := h.settingService.GetOpenAIGrok429ExhaustionSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.OpenAIGrok429ExhaustionSettings{
		Enabled:                  updated.Enabled,
		FreeFullDurationHours:    updated.FreeFullDurationHours,
		FreeFullThresholdPercent: updated.FreeFullThresholdPercent,
		NoResetDurationMinutes:   updated.NoResetDurationMinutes,
	})
}

// GetAccountPoolProbeSettings 获取号池全局异步探测配置
// GET /api/v1/admin/settings/account-pool-probe
func (h *SettingHandler) GetAccountPoolProbeSettings(c *gin.Context) {
	settings, err := h.settingService.GetAccountPoolProbeSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	platforms := settings.Platforms
	if platforms == nil {
		platforms = []string{}
	}
	response.Success(c, dto.AccountPoolProbeSettings{
		Enabled:                settings.Enabled,
		IntervalMinutes:        settings.IntervalMinutes,
		BatchSize:              settings.BatchSize,
		MaxConcurrency:         settings.MaxConcurrency,
		AccountCooldownMinutes: settings.AccountCooldownMinutes,
		Platforms:              platforms,
	})
}

// UpdateAccountPoolProbeSettingsRequest 更新号池全局异步探测配置请求
type UpdateAccountPoolProbeSettingsRequest struct {
	Enabled                bool     `json:"enabled"`
	IntervalMinutes        int      `json:"interval_minutes"`
	BatchSize              int      `json:"batch_size"`
	MaxConcurrency         int      `json:"max_concurrency"`
	AccountCooldownMinutes int      `json:"account_cooldown_minutes"`
	Platforms              []string `json:"platforms"`
}

// UpdateAccountPoolProbeSettings 更新号池全局异步探测配置
// PUT /api/v1/admin/settings/account-pool-probe
func (h *SettingHandler) UpdateAccountPoolProbeSettings(c *gin.Context) {
	var req UpdateAccountPoolProbeSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings := &service.AccountPoolProbeSettings{
		Enabled:                req.Enabled,
		IntervalMinutes:        req.IntervalMinutes,
		BatchSize:              req.BatchSize,
		MaxConcurrency:         req.MaxConcurrency,
		AccountCooldownMinutes: req.AccountCooldownMinutes,
		Platforms:              req.Platforms,
	}
	if err := h.settingService.SetAccountPoolProbeSettings(c.Request.Context(), settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	updated, err := h.settingService.GetAccountPoolProbeSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	platforms := updated.Platforms
	if platforms == nil {
		platforms = []string{}
	}
	response.Success(c, dto.AccountPoolProbeSettings{
		Enabled:                updated.Enabled,
		IntervalMinutes:        updated.IntervalMinutes,
		BatchSize:              updated.BatchSize,
		MaxConcurrency:         updated.MaxConcurrency,
		AccountCooldownMinutes: updated.AccountCooldownMinutes,
		Platforms:              platforms,
	})
}

// GetGrokOpsProxySettings 获取 Grok ops_proxy 配置
// GET /api/v1/admin/settings/grok-ops-proxy
func (h *SettingHandler) GetGrokOpsProxySettings(c *gin.Context) {
	settings, err := h.settingService.GetGrokOpsProxySettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.GrokOpsProxySettings{
		Enabled:        settings.Enabled,
		ProxyID:        settings.ProxyID,
		ApplyToRefresh: settings.ApplyToRefresh,
	})
}

// UpdateGrokOpsProxySettingsRequest 更新 Grok ops_proxy 配置请求
type UpdateGrokOpsProxySettingsRequest struct {
	Enabled        bool   `json:"enabled"`
	ProxyID        *int64 `json:"proxy_id"`
	ApplyToRefresh bool   `json:"apply_to_refresh"`
}

// UpdateGrokOpsProxySettings 更新 Grok ops_proxy 配置
// PUT /api/v1/admin/settings/grok-ops-proxy
func (h *SettingHandler) UpdateGrokOpsProxySettings(c *gin.Context) {
	var req UpdateGrokOpsProxySettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	settings := &service.GrokOpsProxySettings{
		Enabled:        req.Enabled,
		ProxyID:        req.ProxyID,
		ApplyToRefresh: req.ApplyToRefresh,
	}
	if err := h.settingService.SetGrokOpsProxySettings(c.Request.Context(), settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	updated, err := h.settingService.GetGrokOpsProxySettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.GrokOpsProxySettings{
		Enabled:        updated.Enabled,
		ProxyID:        updated.ProxyID,
		ApplyToRefresh: updated.ApplyToRefresh,
	})
}

func dtoGrokCLIIdentityStatus(status *service.GrokCLIIdentityStatus) dto.GrokCLIIdentityStatus {
	if status == nil {
		return dto.GrokCLIIdentityStatus{}
	}
	return dto.GrokCLIIdentityStatus{
		EffectiveVersion: status.EffectiveVersion,
		PinnedDefault:    status.PinnedDefault,
		SettingsOverride: status.SettingsOverride,
		EnvOverride:      status.EnvOverride,
		Source:           status.Source,
		LatestVersion:    status.LatestVersion,
		LatestCheckedAt:  status.LatestCheckedAt,
		UpdateAvailable:  status.UpdateAvailable,
	}
}

// GetGrokCLIIdentitySettings 获取 Grok CLI 身份版本状态
// GET /api/v1/admin/settings/grok-cli-identity
func (h *SettingHandler) GetGrokCLIIdentitySettings(c *gin.Context) {
	status, err := h.settingService.GetGrokCLIIdentityStatus(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dtoGrokCLIIdentityStatus(status))
}

// UpdateGrokCLIIdentitySettingsRequest 更新 Grok CLI 身份版本覆盖
type UpdateGrokCLIIdentitySettingsRequest struct {
	Version string `json:"version"`
}

// UpdateGrokCLIIdentitySettings 设置 Grok CLI 身份版本覆盖（空字符串清除）
// PUT /api/v1/admin/settings/grok-cli-identity
func (h *SettingHandler) UpdateGrokCLIIdentitySettings(c *gin.Context) {
	var req UpdateGrokCLIIdentitySettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.settingService.SetGrokCLIIdentitySettings(c.Request.Context(), &service.GrokCLIIdentitySettings{
		Version: req.Version,
	}); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	status, err := h.settingService.GetGrokCLIIdentityStatus(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dtoGrokCLIIdentityStatus(status))
}

// CheckGrokCLIIdentityLatest 检测 npm @xai-official/grok latest
// POST /api/v1/admin/settings/grok-cli-identity/check
func (h *SettingHandler) CheckGrokCLIIdentityLatest(c *gin.Context) {
	status, err := h.settingService.CheckGrokCLIIdentityLatest(c.Request.Context(), true)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dtoGrokCLIIdentityStatus(status))
}

// ApplyGrokCLIIdentityLatest 检测并将 npm latest 一键写入 settings 覆盖
// POST /api/v1/admin/settings/grok-cli-identity/apply-latest
func (h *SettingHandler) ApplyGrokCLIIdentityLatest(c *gin.Context) {
	status, err := h.settingService.ApplyGrokCLIIdentityLatest(c.Request.Context())
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, dtoGrokCLIIdentityStatus(status))
}

// RestoreGrokCLIIdentityDefault 清除 settings 覆盖，恢复编译默认（仍可被 env 覆盖）
// POST /api/v1/admin/settings/grok-cli-identity/restore-default
func (h *SettingHandler) RestoreGrokCLIIdentityDefault(c *gin.Context) {
	status, err := h.settingService.RestoreGrokCLIIdentityDefault(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dtoGrokCLIIdentityStatus(status))
}

// GetPanelRateLimitSettings 获取面板 API 限流配置
// GET /api/v1/admin/settings/panel-rate-limit
func (h *SettingHandler) GetPanelRateLimitSettings(c *gin.Context) {
	settings, err := h.settingService.GetPanelRateLimitSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.PanelRateLimitSettings{
		Enabled:     settings.Enabled,
		UserRPM:     settings.UserRPM,
		HeavyRPM:    settings.HeavyRPM,
		ExemptAdmin: settings.ExemptAdmin,
		PublicIPRPM: settings.PublicIPRPM,
	})
}

// UpdatePanelRateLimitSettingsRequest 更新面板 API 限流配置请求
type UpdatePanelRateLimitSettingsRequest struct {
	Enabled     bool `json:"enabled"`
	UserRPM     int  `json:"user_rpm"`
	HeavyRPM    int  `json:"heavy_rpm"`
	ExemptAdmin bool `json:"exempt_admin"`
	PublicIPRPM int  `json:"public_ip_rpm"`
}

// UpdatePanelRateLimitSettings 更新面板 API 限流配置
// PUT /api/v1/admin/settings/panel-rate-limit
func (h *SettingHandler) UpdatePanelRateLimitSettings(c *gin.Context) {
	var req UpdatePanelRateLimitSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	settings := &service.PanelRateLimitSettings{
		Enabled:     req.Enabled,
		UserRPM:     req.UserRPM,
		HeavyRPM:    req.HeavyRPM,
		ExemptAdmin: req.ExemptAdmin,
		PublicIPRPM: req.PublicIPRPM,
	}

	if err := h.settingService.SetPanelRateLimitSettings(c.Request.Context(), settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updatedSettings, err := h.settingService.GetPanelRateLimitSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.PanelRateLimitSettings{
		Enabled:     updatedSettings.Enabled,
		UserRPM:     updatedSettings.UserRPM,
		HeavyRPM:    updatedSettings.HeavyRPM,
		ExemptAdmin: updatedSettings.ExemptAdmin,
		PublicIPRPM: updatedSettings.PublicIPRPM,
	})
}

// GetStreamTimeoutSettings 获取流超时处理配置
// GET /api/v1/admin/settings/stream-timeout
func (h *SettingHandler) GetStreamTimeoutSettings(c *gin.Context) {
	settings, err := h.settingService.GetStreamTimeoutSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.StreamTimeoutSettings{
		Enabled:                settings.Enabled,
		Action:                 settings.Action,
		TempUnschedMinutes:     settings.TempUnschedMinutes,
		ThresholdCount:         settings.ThresholdCount,
		ThresholdWindowMinutes: settings.ThresholdWindowMinutes,
	})
}

// GetRectifierSettings 获取请求整流器配置
// GET /api/v1/admin/settings/rectifier
func (h *SettingHandler) GetRectifierSettings(c *gin.Context) {
	settings, err := h.settingService.GetRectifierSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	patterns := settings.APIKeySignaturePatterns
	if patterns == nil {
		patterns = []string{}
	}
	response.Success(c, dto.RectifierSettings{
		Enabled:                  settings.Enabled,
		ThinkingSignatureEnabled: settings.ThinkingSignatureEnabled,
		ThinkingBudgetEnabled:    settings.ThinkingBudgetEnabled,
		APIKeySignatureEnabled:   settings.APIKeySignatureEnabled,
		APIKeySignaturePatterns:  patterns,
	})
}

// UpdateRectifierSettingsRequest 更新整流器配置请求
type UpdateRectifierSettingsRequest struct {
	Enabled                  bool     `json:"enabled"`
	ThinkingSignatureEnabled bool     `json:"thinking_signature_enabled"`
	ThinkingBudgetEnabled    bool     `json:"thinking_budget_enabled"`
	APIKeySignatureEnabled   bool     `json:"apikey_signature_enabled"`
	APIKeySignaturePatterns  []string `json:"apikey_signature_patterns"`
}

// UpdateRectifierSettings 更新请求整流器配置
// PUT /api/v1/admin/settings/rectifier
func (h *SettingHandler) UpdateRectifierSettings(c *gin.Context) {
	var req UpdateRectifierSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// 校验并清理自定义匹配关键词
	const maxPatterns = 50
	const maxPatternLen = 500
	if len(req.APIKeySignaturePatterns) > maxPatterns {
		response.BadRequest(c, "Too many signature patterns (max 50)")
		return
	}
	var cleanedPatterns []string
	for _, p := range req.APIKeySignaturePatterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if len(p) > maxPatternLen {
			response.BadRequest(c, "Signature pattern too long (max 500 characters)")
			return
		}
		cleanedPatterns = append(cleanedPatterns, p)
	}

	settings := &service.RectifierSettings{
		Enabled:                  req.Enabled,
		ThinkingSignatureEnabled: req.ThinkingSignatureEnabled,
		ThinkingBudgetEnabled:    req.ThinkingBudgetEnabled,
		APIKeySignatureEnabled:   req.APIKeySignatureEnabled,
		APIKeySignaturePatterns:  cleanedPatterns,
	}

	if err := h.settingService.SetRectifierSettings(c.Request.Context(), settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// 重新获取设置返回
	updatedSettings, err := h.settingService.GetRectifierSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	updatedPatterns := updatedSettings.APIKeySignaturePatterns
	if updatedPatterns == nil {
		updatedPatterns = []string{}
	}
	response.Success(c, dto.RectifierSettings{
		Enabled:                  updatedSettings.Enabled,
		ThinkingSignatureEnabled: updatedSettings.ThinkingSignatureEnabled,
		ThinkingBudgetEnabled:    updatedSettings.ThinkingBudgetEnabled,
		APIKeySignatureEnabled:   updatedSettings.APIKeySignatureEnabled,
		APIKeySignaturePatterns:  updatedPatterns,
	})
}

// GetBetaPolicySettings 获取 Beta 策略配置
// GET /api/v1/admin/settings/beta-policy
func (h *SettingHandler) GetBetaPolicySettings(c *gin.Context) {
	settings, err := h.settingService.GetBetaPolicySettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	rules := make([]dto.BetaPolicyRule, len(settings.Rules))
	for i, r := range settings.Rules {
		rules[i] = dto.BetaPolicyRule(r)
	}
	response.Success(c, dto.BetaPolicySettings{Rules: rules})
}

// UpdateBetaPolicySettingsRequest 更新 Beta 策略配置请求
type UpdateBetaPolicySettingsRequest struct {
	Rules []dto.BetaPolicyRule `json:"rules"`
}

// UpdateBetaPolicySettings 更新 Beta 策略配置
// PUT /api/v1/admin/settings/beta-policy
func (h *SettingHandler) UpdateBetaPolicySettings(c *gin.Context) {
	var req UpdateBetaPolicySettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	rules := make([]service.BetaPolicyRule, len(req.Rules))
	for i, r := range req.Rules {
		rules[i] = service.BetaPolicyRule(r)
	}

	settings := &service.BetaPolicySettings{Rules: rules}
	if err := h.settingService.SetBetaPolicySettings(c.Request.Context(), settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Re-fetch to return updated settings
	updated, err := h.settingService.GetBetaPolicySettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	outRules := make([]dto.BetaPolicyRule, len(updated.Rules))
	for i, r := range updated.Rules {
		outRules[i] = dto.BetaPolicyRule(r)
	}
	response.Success(c, dto.BetaPolicySettings{Rules: outRules})
}

// GetGrokReasoningVisibilitySettings 获取网关级 Grok 思考明文调度配置
// GET /api/v1/admin/settings/grok-reasoning-visibility
func (h *SettingHandler) GetGrokReasoningVisibilitySettings(c *gin.Context) {
	settings, err := h.settingService.GetGrokReasoningVisibilitySettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.GrokReasoningVisibilitySettings{
		Mode:                 settings.Mode,
		ProbeTTLSec:          settings.ProbeTTLSec,
		QuarantineSec:        settings.QuarantineSec,
		ProbeAccountFallback: settings.ProbeAccountFallback,
	})
}

// UpdateGrokReasoningVisibilitySettingsRequest 更新 Grok 思考明文调度配置请求
type UpdateGrokReasoningVisibilitySettingsRequest struct {
	Mode                 string `json:"mode"`
	ProbeTTLSec          int    `json:"probe_ttl_sec"`
	QuarantineSec        int    `json:"quarantine_sec"`
	ProbeAccountFallback bool   `json:"probe_account_fallback"`
}

// UpdateGrokReasoningVisibilitySettings 更新网关级 Grok 思考明文调度配置
// PUT /api/v1/admin/settings/grok-reasoning-visibility
func (h *SettingHandler) UpdateGrokReasoningVisibilitySettings(c *gin.Context) {
	var req UpdateGrokReasoningVisibilitySettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if err := h.settingService.SetGrokReasoningVisibilitySettings(c.Request.Context(), &service.GrokReasoningVisibilitySettings{
		Mode:                 req.Mode,
		ProbeTTLSec:          req.ProbeTTLSec,
		QuarantineSec:        req.QuarantineSec,
		ProbeAccountFallback: req.ProbeAccountFallback,
	}); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updated, err := h.settingService.GetGrokReasoningVisibilitySettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.GrokReasoningVisibilitySettings{
		Mode:                 updated.Mode,
		ProbeTTLSec:          updated.ProbeTTLSec,
		QuarantineSec:        updated.QuarantineSec,
		ProbeAccountFallback: updated.ProbeAccountFallback,
	})
}

// GetGrokToolPromptSettings 获取网关级 Grok 工具提示注入配置
// GET /api/v1/admin/settings/grok-tool-prompt
func (h *SettingHandler) GetGrokToolPromptSettings(c *gin.Context) {
	settings, err := h.settingService.GetGrokToolPromptSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, service.GrokToolPromptSettings{
		Enabled: settings.Enabled,
		Prompt:  settings.Prompt,
	})
}

// UpdateGrokToolPromptSettingsRequest 更新 Grok 工具提示注入配置请求
type UpdateGrokToolPromptSettingsRequest struct {
	Enabled bool   `json:"enabled"`
	Prompt  string `json:"prompt"`
}

// UpdateGrokToolPromptSettings 更新网关级 Grok 工具提示注入配置
// PUT /api/v1/admin/settings/grok-tool-prompt
func (h *SettingHandler) UpdateGrokToolPromptSettings(c *gin.Context) {
	var req UpdateGrokToolPromptSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.settingService.SetGrokToolPromptSettings(c.Request.Context(), &service.GrokToolPromptSettings{
		Enabled: req.Enabled,
		Prompt:  req.Prompt,
	}); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	updated, err := h.settingService.GetGrokToolPromptSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, service.GrokToolPromptSettings{
		Enabled: updated.Enabled,
		Prompt:  updated.Prompt,
	})
}

// UpdateStreamTimeoutSettingsRequest 更新流超时配置请求
type UpdateStreamTimeoutSettingsRequest struct {
	Enabled                bool   `json:"enabled"`
	Action                 string `json:"action"`
	TempUnschedMinutes     int    `json:"temp_unsched_minutes"`
	ThresholdCount         int    `json:"threshold_count"`
	ThresholdWindowMinutes int    `json:"threshold_window_minutes"`
}

// UpdateStreamTimeoutSettings 更新流超时处理配置
// PUT /api/v1/admin/settings/stream-timeout
func (h *SettingHandler) UpdateStreamTimeoutSettings(c *gin.Context) {
	var req UpdateStreamTimeoutSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	settings := &service.StreamTimeoutSettings{
		Enabled:                req.Enabled,
		Action:                 req.Action,
		TempUnschedMinutes:     req.TempUnschedMinutes,
		ThresholdCount:         req.ThresholdCount,
		ThresholdWindowMinutes: req.ThresholdWindowMinutes,
	}

	if err := h.settingService.SetStreamTimeoutSettings(c.Request.Context(), settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// 重新获取设置返回
	updatedSettings, err := h.settingService.GetStreamTimeoutSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.StreamTimeoutSettings{
		Enabled:                updatedSettings.Enabled,
		Action:                 updatedSettings.Action,
		TempUnschedMinutes:     updatedSettings.TempUnschedMinutes,
		ThresholdCount:         updatedSettings.ThresholdCount,
		ThresholdWindowMinutes: updatedSettings.ThresholdWindowMinutes,
	})
}

// GetWebSearchEmulationConfig 获取 Web Search 模拟配置
// GET /api/v1/admin/settings/web-search-emulation
func (h *SettingHandler) GetWebSearchEmulationConfig(c *gin.Context) {
	cfg, err := h.settingService.GetWebSearchEmulationConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, service.PopulateWebSearchUsage(c.Request.Context(), cfg))
}

// UpdateWebSearchEmulationConfig 更新 Web Search 模拟配置
// PUT /api/v1/admin/settings/web-search-emulation
func (h *SettingHandler) UpdateWebSearchEmulationConfig(c *gin.Context) {
	var cfg service.WebSearchEmulationConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if err := h.settingService.SaveWebSearchEmulationConfig(c.Request.Context(), &cfg); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Re-read (with sanitized api keys) to return current state
	updated, err := h.settingService.GetWebSearchEmulationConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, service.PopulateWebSearchUsage(c.Request.Context(), updated))
}

// ResetWebSearchUsage 重置指定 provider 的配额用量
// POST /api/v1/admin/settings/web-search-emulation/reset-usage
func (h *SettingHandler) ResetWebSearchUsage(c *gin.Context) {
	var req struct {
		ProviderType string `json:"provider_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.ProviderType == "" {
		response.BadRequest(c, "provider_type is required")
		return
	}
	if err := service.ResetWebSearchUsage(c.Request.Context(), req.ProviderType); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, nil)
}

// TestWebSearchEmulation 测试 Web Search 搜索
// POST /api/v1/admin/settings/web-search-emulation/test
func (h *SettingHandler) TestWebSearchEmulation(c *gin.Context) {
	var req struct {
		Query string `json:"query"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		req.Query = "搜索今年世界大事件"
	}

	result, err := service.TestWebSearch(c.Request.Context(), req.Query)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

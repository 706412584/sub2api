package admin

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// CodeBuddy OAuth 登录 handler（state 设备流 + 建号）。
// 路由挂在 /api/v1/admin/codebuddy 下。
type CodebuddyOAuthHandler struct {
	adminService       service.AdminService
	codebuddyLogin     *service.CodebuddyLoginService
	accountTestService *service.AccountTestService
	codebuddyGateway   *service.CodebuddyGatewayService
}

func NewCodebuddyOAuthHandler(
	adminService service.AdminService,
	codebuddyLogin *service.CodebuddyLoginService,
	accountTestService *service.AccountTestService,
	codebuddyGateway *service.CodebuddyGatewayService,
) *CodebuddyOAuthHandler {
	return &CodebuddyOAuthHandler{
		adminService:       adminService,
		codebuddyLogin:     codebuddyLogin,
		accountTestService: accountTestService,
		codebuddyGateway:   codebuddyGateway,
	}
}

type CodebuddyLoginStartRequest struct {
	Region string `json:"region"` // "cn"（缺省）或 "global"
}

type CodebuddyLoginPollRequest struct {
	SessionID string `json:"session_id" binding:"required"`
}

type CodebuddyOAuthCreateRequest struct {
	SessionID            string   `json:"session_id" binding:"required"`
	Name                 string   `json:"name"`
	Notes                *string  `json:"notes"`
	ProxyID              *int64   `json:"proxy_id"`
	Concurrency          *int     `json:"concurrency"`
	Priority             *int     `json:"priority"`
	RateMultiplier       *float64 `json:"rate_multiplier"`
	LoadFactor           *int     `json:"load_factor"`
	GroupIDs             []int64  `json:"group_ids"`
	SkipDefaultGroupBind *bool    `json:"skip_default_group_bind"`
}

// StartLogin 申请 OAuth state 并返回授权 URL。
// POST /api/v1/admin/codebuddy/oauth/start
func (h *CodebuddyOAuthHandler) StartLogin(c *gin.Context) {
	var req CodebuddyLoginStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req = CodebuddyLoginStartRequest{}
	}
	result, err := h.codebuddyLogin.Start(c.Request.Context(), req.Region)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// PollLogin 轮询登录状态一次。
// POST /api/v1/admin/codebuddy/oauth/poll
func (h *CodebuddyOAuthHandler) PollLogin(c *gin.Context) {
	var req CodebuddyLoginPollRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	result, err := h.codebuddyLogin.Poll(c.Request.Context(), req.SessionID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// CreateAccountFromLogin 用已完成授权的登录会话建号。
// POST /api/v1/admin/codebuddy/oauth/create-account
func (h *CodebuddyOAuthHandler) CreateAccountFromLogin(c *gin.Context) {
	var req CodebuddyOAuthCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	credentials, err := h.codebuddyLogin.Credentials(req.SessionID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	accountType := service.AccountTypeCodebuddyCN
	if credentials.Region == codebuddy.RegionGlobal {
		accountType = service.AccountTypeCodebuddyGlob
	}

	creds := codebuddyLoginCredentialsToMap(credentials)
	name := buildCodebuddyAccountName(req.Name, credentials, creds)

	account, err := h.createCodebuddyAccount(c.Request.Context(), createCodebuddyAccountParams{
		name:                 name,
		accountType:          accountType,
		credentials:          creds,
		notes:                req.Notes,
		proxyID:              req.ProxyID,
		concurrency:          req.Concurrency,
		priority:             req.Priority,
		rateMultiplier:       req.RateMultiplier,
		loadFactor:           req.LoadFactor,
		groupIDs:             req.GroupIDs,
		skipDefaultGroupBind: req.SkipDefaultGroupBind,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.AccountFromService(account))
}

// buildCodebuddyAccountName 生成默认账号名：用户指定 > nickname > uid > codebuddy-cn/global-N。
func buildCodebuddyAccountName(requested string, credentials *service.CodebuddyLoginCredentials, _ map[string]any) string {
	if strings.TrimSpace(requested) != "" {
		return strings.TrimSpace(requested)
	}
	for _, candidate := range []string{credentials.Nickname, credentials.UID} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	if credentials.Region == codebuddy.RegionGlobal {
		return "codebuddy-global"
	}
	return "codebuddy-cn"
}

// codebuddyLoginCredentialsToMap 把登录会话凭证展开为 Account.Credentials 子键。
func codebuddyLoginCredentialsToMap(credentials *service.CodebuddyLoginCredentials) map[string]any {
	out := map[string]any{
		"access_token": credentials.AccessToken,
	}
	if strings.TrimSpace(credentials.RefreshToken) != "" {
		out["refresh_token"] = credentials.RefreshToken
	}
	if credentials.ExpiresAt > 0 {
		out["expires_at"] = credentials.ExpiresAt
	}
	if strings.TrimSpace(credentials.Domain) != "" {
		out["domain"] = credentials.Domain
	}
	if strings.TrimSpace(credentials.UID) != "" {
		out["uid"] = credentials.UID
	}
	if strings.TrimSpace(credentials.EnterpriseID) != "" {
		out["enterprise_id"] = credentials.EnterpriseID
	}
	if strings.TrimSpace(credentials.Nickname) != "" {
		out["nickname"] = credentials.Nickname
	}
	return out
}

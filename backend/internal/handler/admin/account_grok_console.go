package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Grok Console / Web 会话凭据管理 API。
// 会话材料（SSO、cf_clearance、UA）只进专用加密表，绝不回显。

type grokConsoleSessionRequest struct {
	SSOToken         string `json:"sso_token" binding:"required"`
	SSORWToken       string `json:"sso_rw_token,omitempty"`
	BrowserUserAgent string `json:"browser_user_agent" binding:"required"`
	ProxyID          *int64 `json:"proxy_id" binding:"required"`
}

type grokWebSessionRequest struct {
	SSOToken         string `json:"sso_token" binding:"required"`
	SSORWToken       string `json:"sso_rw_token,omitempty"`
	CFClearance      string `json:"cf_clearance" binding:"required"`
	BrowserUserAgent string `json:"browser_user_agent" binding:"required"`
	ProxyID          *int64 `json:"proxy_id" binding:"required"`
}

func grokSessionAccountID(c *gin.Context) (int64, bool) {
	raw := c.Param("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid account id")
		return 0, false
	}
	return id, true
}

// SaveGrokConsoleSession 保存 Console 会话（POST /admin/accounts/:id/grok-console-session）
func (h *AccountHandler) SaveGrokConsoleSession(c *gin.Context) {
	if h.grokSession == nil {
		response.ErrorFrom(c, service.ErrGrokSessionUnavailable)
		return
	}
	accountID, ok := grokSessionAccountID(c)
	if !ok {
		return
	}
	var req grokConsoleSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.grokSession.SaveConsoleSession(
		c.Request.Context(), accountID,
		req.SSOToken, req.SSORWToken, req.BrowserUserAgent, req.ProxyID,
	); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	status, err := h.grokSession.GetSessionStatus(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

// GetGrokConsoleSession 获取 Console 会话状态（GET /admin/accounts/:id/grok-console-session）
func (h *AccountHandler) GetGrokConsoleSession(c *gin.Context) {
	if h.grokSession == nil {
		response.ErrorFrom(c, service.ErrGrokSessionUnavailable)
		return
	}
	accountID, ok := grokSessionAccountID(c)
	if !ok {
		return
	}
	status, err := h.grokSession.GetSessionStatus(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

// DeleteGrokConsoleSession 删除 Console 会话（DELETE /admin/accounts/:id/grok-console-session）
func (h *AccountHandler) DeleteGrokConsoleSession(c *gin.Context) {
	if h.grokSession == nil {
		response.ErrorFrom(c, service.ErrGrokSessionUnavailable)
		return
	}
	accountID, ok := grokSessionAccountID(c)
	if !ok {
		return
	}
	if err := h.grokSession.DeleteSession(c.Request.Context(), accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// SaveGrokWebSession 保存 Web 会话（POST /admin/accounts/:id/grok-web-session）
func (h *AccountHandler) SaveGrokWebSession(c *gin.Context) {
	if h.grokSession == nil {
		response.ErrorFrom(c, service.ErrGrokSessionUnavailable)
		return
	}
	accountID, ok := grokSessionAccountID(c)
	if !ok {
		return
	}
	var req grokWebSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.grokSession.SaveWebSession(
		c.Request.Context(), accountID,
		req.SSOToken, req.SSORWToken, req.CFClearance, req.BrowserUserAgent, req.ProxyID,
	); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	status, err := h.grokSession.GetSessionStatus(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

// GetGrokWebSession 获取 Web 会话状态（GET /admin/accounts/:id/grok-web-session）
func (h *AccountHandler) GetGrokWebSession(c *gin.Context) {
	if h.grokSession == nil {
		response.ErrorFrom(c, service.ErrGrokSessionUnavailable)
		return
	}
	accountID, ok := grokSessionAccountID(c)
	if !ok {
		return
	}
	status, err := h.grokSession.GetSessionStatus(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

// DeleteGrokWebSession 删除 Web 会话（DELETE /admin/accounts/:id/grok-web-session）
func (h *AccountHandler) DeleteGrokWebSession(c *gin.Context) {
	if h.grokSession == nil {
		response.ErrorFrom(c, service.ErrGrokSessionUnavailable)
		return
	}
	accountID, ok := grokSessionAccountID(c)
	if !ok {
		return
	}
	if err := h.grokSession.DeleteSession(c.Request.Context(), accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

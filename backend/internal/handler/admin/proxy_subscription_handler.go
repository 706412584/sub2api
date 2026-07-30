package admin

import (
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ProxySubscriptionHandler manages embedded subscription sources.
type ProxySubscriptionHandler struct {
	svc *service.ProxySubscriptionService
}

// NewProxySubscriptionHandler constructs the handler.
func NewProxySubscriptionHandler(svc *service.ProxySubscriptionService) *ProxySubscriptionHandler {
	return &ProxySubscriptionHandler{svc: svc}
}

type proxySubscriptionCreateRequest struct {
	Name              string   `json:"name" binding:"required,max=100"`
	Enabled           *bool    `json:"enabled"`
	SourceType        string   `json:"source_type" binding:"omitempty,oneof=url inline"`
	SubscriptionURL   string   `json:"subscription_url" binding:"omitempty,max=2000"`
	InlineBody        string   `json:"inline_body"`
	NamePrefix        string   `json:"name_prefix" binding:"omitempty,max=40"`
	Protocol          string   `json:"protocol" binding:"omitempty,oneof=socks5 socks5h http https"`
	BindAddress       string   `json:"bind_address" binding:"omitempty,max=64"`
	BasePort          int      `json:"base_port" binding:"omitempty,min=1024,max=65535"`
	MaxPorts          int      `json:"max_ports" binding:"omitempty,min=1,max=64"`
	SyncIntervalSec   int      `json:"sync_interval_sec" binding:"omitempty,min=60,max=86400"`
	NodeAllowContains []string `json:"node_allow_contains"`
}

type proxySubscriptionUpdateRequest struct {
	Name              *string   `json:"name" binding:"omitempty,max=100"`
	Enabled           *bool     `json:"enabled"`
	SourceType        *string   `json:"source_type" binding:"omitempty,oneof=url inline"`
	SubscriptionURL   *string   `json:"subscription_url" binding:"omitempty,max=2000"`
	InlineBody        *string   `json:"inline_body"`
	NamePrefix        *string   `json:"name_prefix" binding:"omitempty,max=40"`
	Protocol          *string   `json:"protocol" binding:"omitempty,oneof=socks5 socks5h http https"`
	BindAddress       *string   `json:"bind_address" binding:"omitempty,max=64"`
	BasePort          *int      `json:"base_port" binding:"omitempty,min=1024,max=65535"`
	MaxPorts          *int      `json:"max_ports" binding:"omitempty,min=1,max=64"`
	SyncIntervalSec   *int      `json:"sync_interval_sec" binding:"omitempty,min=60,max=86400"`
	NodeAllowContains *[]string `json:"node_allow_contains"`
}

type proxySubscriptionResponse struct {
	ID                   int64     `json:"id"`
	Name                 string    `json:"name"`
	Enabled              bool      `json:"enabled"`
	SourceType           string    `json:"source_type"`
	SubscriptionURLMasked string   `json:"subscription_url_masked"`
	HasInlineBody        bool      `json:"has_inline_body"`
	NamePrefix           string    `json:"name_prefix"`
	Protocol             string    `json:"protocol"`
	BindAddress          string    `json:"bind_address"`
	BasePort             int       `json:"base_port"`
	MaxPorts             int       `json:"max_ports"`
	SyncIntervalSec      int       `json:"sync_interval_sec"`
	NodeAllowContains    []string  `json:"node_allow_contains"`
	LastSyncAt           *string   `json:"last_sync_at"`
	LastSyncStatus       string    `json:"last_sync_status"`
	LastSyncError        string    `json:"last_sync_error"`
	LastConfigHash       string    `json:"last_config_hash"`
	DesiredCount         int       `json:"desired_count"`
	CreatedBy            int64     `json:"created_by"`
	NextDueAt            *string   `json:"next_due_at"`
	CreatedAt            string    `json:"created_at"`
	UpdatedAt            string    `json:"updated_at"`
}

// List GET /admin/proxy-subscriptions
func (h *ProxySubscriptionHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	params := service.ProxySubscriptionListParams{
		Page:     page,
		PageSize: pageSize,
		Search:   strings.TrimSpace(c.Query("search")),
	}
	if v := c.Query("enabled"); v != "" {
		b := v == "1" || strings.EqualFold(v, "true")
		params.Enabled = &b
	}
	items, total, err := h.svc.List(c.Request.Context(), params)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]proxySubscriptionResponse, 0, len(items))
	for _, m := range items {
		out = append(out, toProxySubscriptionResponse(m))
	}
	response.Paginated(c, out, total, page, pageSize)
}

// Create POST /admin/proxy-subscriptions
func (h *ProxySubscriptionHandler) Create(c *gin.Context) {
	var req proxySubscriptionCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	createdBy := int64(0)
	if subject, ok := middleware2.GetAuthSubjectFromContext(c); ok {
		createdBy = subject.UserID
	}
	m, err := h.svc.Create(c.Request.Context(), service.ProxySubscriptionCreateParams{
		Name:              req.Name,
		Enabled:           req.Enabled,
		SourceType:        req.SourceType,
		SubscriptionURL:   req.SubscriptionURL,
		InlineBody:        req.InlineBody,
		NamePrefix:        req.NamePrefix,
		Protocol:          req.Protocol,
		BindAddress:       req.BindAddress,
		BasePort:          req.BasePort,
		MaxPorts:          req.MaxPorts,
		SyncIntervalSec:   req.SyncIntervalSec,
		NodeAllowContains: req.NodeAllowContains,
		CreatedBy:         createdBy,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, toProxySubscriptionResponse(m))
}

// Get GET /admin/proxy-subscriptions/:id
func (h *ProxySubscriptionHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid id")
		return
	}
	m, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, toProxySubscriptionResponse(m))
}

// Update PUT /admin/proxy-subscriptions/:id
func (h *ProxySubscriptionHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid id")
		return
	}
	var req proxySubscriptionUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	m, err := h.svc.Update(c.Request.Context(), id, service.ProxySubscriptionUpdateParams{
		Name:              req.Name,
		Enabled:           req.Enabled,
		SourceType:        req.SourceType,
		SubscriptionURL:   req.SubscriptionURL,
		InlineBody:        req.InlineBody,
		NamePrefix:        req.NamePrefix,
		Protocol:          req.Protocol,
		BindAddress:       req.BindAddress,
		BasePort:          req.BasePort,
		MaxPorts:          req.MaxPorts,
		SyncIntervalSec:   req.SyncIntervalSec,
		NodeAllowContains: req.NodeAllowContains,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, toProxySubscriptionResponse(m))
}

// Delete DELETE /admin/proxy-subscriptions/:id
func (h *ProxySubscriptionHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid id")
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// Sync POST /admin/proxy-subscriptions/:id/sync
func (h *ProxySubscriptionHandler) Sync(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid id")
		return
	}
	res, err := h.svc.SyncNow(c.Request.Context(), id)
	if err != nil {
		// Prefer structured app errors; otherwise wrap with message only.
		if response.ErrorFrom(c, err) {
			_ = res // partial result already reflected in last_sync_* on the source
			return
		}
		response.ErrorFrom(c, infraerrors.InternalServer("PROXY_SUBSCRIPTION_SYNC_FAILED", err.Error()))
		return
	}
	response.Success(c, res)
}

// EngineStatus GET /admin/proxy-subscriptions/engine/status
func (h *ProxySubscriptionHandler) EngineStatus(c *gin.Context) {
	st, err := h.svc.EngineStatus(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, st)
}

func toProxySubscriptionResponse(m *service.ProxySubscription) proxySubscriptionResponse {
	if m == nil {
		return proxySubscriptionResponse{}
	}
	allow := m.NodeAllowContains
	if allow == nil {
		allow = []string{}
	}
	return proxySubscriptionResponse{
		ID:                    m.ID,
		Name:                  m.Name,
		Enabled:               m.Enabled,
		SourceType:            m.SourceType,
		SubscriptionURLMasked: service.MaskSubscriptionURL(m.SubscriptionURL),
		HasInlineBody:         strings.TrimSpace(m.InlineBody) != "",
		NamePrefix:            m.NamePrefix,
		Protocol:              m.Protocol,
		BindAddress:           m.BindAddress,
		BasePort:              m.BasePort,
		MaxPorts:              m.MaxPorts,
		SyncIntervalSec:       m.SyncIntervalSec,
		NodeAllowContains:     allow,
		LastSyncAt:            formatTimePtr(m.LastSyncAt),
		LastSyncStatus:        m.LastSyncStatus,
		LastSyncError:         m.LastSyncError,
		LastConfigHash:        m.LastConfigHash,
		DesiredCount:          m.DesiredCount,
		CreatedBy:             m.CreatedBy,
		NextDueAt:             formatTimePtr(m.NextDueAt),
		CreatedAt:             m.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:             m.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

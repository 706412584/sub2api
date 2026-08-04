package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// DynamicProxyPoolHandler handles admin dynamic proxy pool management.
type DynamicProxyPoolHandler struct {
	svc *service.DynamicProxyPoolService
}

// NewDynamicProxyPoolHandler constructs the handler.
func NewDynamicProxyPoolHandler(svc *service.DynamicProxyPoolService) *DynamicProxyPoolHandler {
	return &DynamicProxyPoolHandler{svc: svc}
}

// List returns paginated dynamic proxy pools.
func (h *DynamicProxyPoolHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	search := c.Query("search")

	var enabled *bool
	if v := c.Query("enabled"); v != "" {
		b := v == "true" || v == "1"
		enabled = &b
	}

	pools, total, err := h.svc.List(c.Request.Context(), service.DynamicProxyPoolListParams{
		Page:     page,
		PageSize: pageSize,
		Search:   search,
		Enabled:  enabled,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"items":     pools,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// Create creates a new dynamic proxy pool.
func (h *DynamicProxyPoolHandler) Create(c *gin.Context) {
	var req struct {
		Name               string `json:"name" binding:"required"`
		SourceType         string `json:"source_type"`
		SubscriptionID     *int64 `json:"subscription_id"`
		ExtractURL         string `json:"extract_url"`
		Protocol           string `json:"protocol"`
		AuthMode           string `json:"auth_mode"`
		Username           string `json:"username"`
		Password           string `json:"password"`
		ResponseFormat     string `json:"response_format"`
		LineSeparator      string `json:"line_separator"`
		IPFieldPath        string `json:"ip_field_path"`
		PortFieldPath      string `json:"port_field_path"`
		RefreshIntervalSec int    `json:"refresh_interval_sec"`
		IPDurationSec      int    `json:"ip_duration_sec"`
		ExtractCount       int    `json:"extract_count"`
		MinAlive           int    `json:"min_alive"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	pool, err := h.svc.Create(c.Request.Context(), service.DynamicProxyPoolCreateParams{
		Name:               req.Name,
		SourceType:         req.SourceType,
		SubscriptionID:     req.SubscriptionID,
		ExtractURL:         req.ExtractURL,
		Protocol:           req.Protocol,
		AuthMode:           req.AuthMode,
		Username:           req.Username,
		Password:           req.Password,
		ResponseFormat:     req.ResponseFormat,
		LineSeparator:      req.LineSeparator,
		IPFieldPath:        req.IPFieldPath,
		PortFieldPath:      req.PortFieldPath,
		RefreshIntervalSec: req.RefreshIntervalSec,
		IPDurationSec:      req.IPDurationSec,
		ExtractCount:       req.ExtractCount,
		MinAlive:           req.MinAlive,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, pool)
}

// Get returns a single dynamic proxy pool.
func (h *DynamicProxyPoolHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid pool ID")
		return
	}
	pool, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, pool)
}

// Update modifies an existing dynamic proxy pool.
func (h *DynamicProxyPoolHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid pool ID")
		return
	}
	var req struct {
		Name               *string `json:"name"`
		Enabled            *bool   `json:"enabled"`
		SourceType         *string `json:"source_type"`
		SubscriptionID     *int64  `json:"subscription_id"`
		ExtractURL         *string `json:"extract_url"`
		Protocol           *string `json:"protocol"`
		AuthMode           *string `json:"auth_mode"`
		Username           *string `json:"username"`
		Password           *string `json:"password"`
		ResponseFormat     *string `json:"response_format"`
		LineSeparator      *string `json:"line_separator"`
		IPFieldPath        *string `json:"ip_field_path"`
		PortFieldPath      *string `json:"port_field_path"`
		RefreshIntervalSec *int    `json:"refresh_interval_sec"`
		IPDurationSec      *int    `json:"ip_duration_sec"`
		ExtractCount       *int    `json:"extract_count"`
		MinAlive           *int    `json:"min_alive"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	pool, err := h.svc.Update(c.Request.Context(), id, service.DynamicProxyPoolUpdateParams{
		Name:               req.Name,
		Enabled:            req.Enabled,
		SourceType:         req.SourceType,
		SubscriptionID:     req.SubscriptionID,
		ExtractURL:         req.ExtractURL,
		Protocol:           req.Protocol,
		AuthMode:           req.AuthMode,
		Username:           req.Username,
		Password:           req.Password,
		ResponseFormat:     req.ResponseFormat,
		LineSeparator:      req.LineSeparator,
		IPFieldPath:        req.IPFieldPath,
		PortFieldPath:      req.PortFieldPath,
		RefreshIntervalSec: req.RefreshIntervalSec,
		IPDurationSec:      req.IPDurationSec,
		ExtractCount:       req.ExtractCount,
		MinAlive:           req.MinAlive,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, pool)
}

// Delete removes a dynamic proxy pool.
func (h *DynamicProxyPoolHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid pool ID")
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, nil)
}

// Extract triggers an immediate IP extraction.
func (h *DynamicProxyPoolHandler) Extract(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid pool ID")
		return
	}
	result, err := h.svc.Extract(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

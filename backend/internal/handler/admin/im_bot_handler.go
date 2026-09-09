package admin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Wei-Shaw/sub2api/internal/im"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// IMBotHandler serves /api/v1/admin/im-bots. Credentials are write-only:
// responses never echo them.
type IMBotHandler struct {
	imBotService *service.IMBotService
	hubRef       im.HubRef
}

// NewIMBotHandler wires the handler. hubRef is im.HubRef (concrete struct,
// methods are value receivers) injected via the im ProviderSet.
func NewIMBotHandler(imBotService *service.IMBotService, hubRef im.HubRef) *IMBotHandler {
	return &IMBotHandler{imBotService: imBotService, hubRef: hubRef}
}

func pageParams(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	return page, pageSize
}

func mapErr(c *gin.Context, err error) {
	if response.ErrorFrom(c, err) {
		return
	}
	response.Error(c, http.StatusInternalServerError, err.Error())
}

// List GET /im-bots
func (h *IMBotHandler) List(c *gin.Context) {
	page, pageSize := pageParams(c)
	bots, pg, err := h.imBotService.List(c.Request.Context(), page, pageSize,
		c.Query("platform"), c.Query("status"), c.Query("search"),
		c.Query("sort_by"), c.Query("sort_order"))
	if err != nil {
		mapErr(c, err)
		return
	}
	response.Success(c, gin.H{"items": bots, "pagination": pg})
}

// GetByID GET /im-bots/:id
func (h *IMBotHandler) GetByID(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	bot, err := h.imBotService.GetByID(c.Request.Context(), id)
	if err != nil {
		mapErr(c, err)
		return
	}
	response.Success(c, bot)
}

// Create POST /im-bots
func (h *IMBotHandler) Create(c *gin.Context) {
	var req service.CreateIMBotInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request: "+err.Error())
		return
	}
	bot, err := h.imBotService.Create(c.Request.Context(), &req)
	if err != nil {
		mapErr(c, err)
		return
	}
	response.Success(c, bot)
}

// Update PUT /im-bots/:id
func (h *IMBotHandler) Update(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.UpdateIMBotInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request: "+err.Error())
		return
	}
	bot, err := h.imBotService.Update(c.Request.Context(), id, &req)
	if err != nil {
		mapErr(c, err)
		return
	}
	response.Success(c, bot)
}

// Delete DELETE /im-bots/:id
func (h *IMBotHandler) Delete(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.imBotService.Delete(c.Request.Context(), id); err != nil {
		mapErr(c, err)
		return
	}
	response.Success(c, gin.H{"message": "deleted"})
}

// Enable POST /im-bots/:id/enable
func (h *IMBotHandler) Enable(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	bot, err := h.imBotService.SetEnabled(c.Request.Context(), id, true)
	if err != nil {
		mapErr(c, err)
		return
	}
	response.Success(c, bot)
}

// Disable POST /im-bots/:id/disable
func (h *IMBotHandler) Disable(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	bot, err := h.imBotService.SetEnabled(c.Request.Context(), id, false)
	if err != nil {
		mapErr(c, err)
		return
	}
	response.Success(c, bot)
}

// Test POST /im-bots/:id/test — validate stored credentials against the platform.
func (h *IMBotHandler) Test(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	bot, err := h.imBotService.GetByID(c.Request.Context(), id)
	if err != nil {
		mapErr(c, err)
		return
	}
	if !h.hubRef.Enabled() {
		response.Error(c, http.StatusServiceUnavailable, "im hub is not running")
		return
	}
	if err := h.hubRef.TestConnection(c.Request.Context(), bot); err != nil {
		response.Error(c, http.StatusBadGateway, "test failed: "+err.Error())
		return
	}
	response.Success(c, gin.H{"success": true, "message": "connection ok"})
}

// Platforms GET /im-bots/platforms — credential schemas for the wizard.
func (h *IMBotHandler) Platforms(c *gin.Context) {
	response.Success(c, gin.H{"platforms": h.hubRef.PlatformSchemas()})
}

// GeneratePairCode POST /im-bots/:id/pair-codes
func (h *IMBotHandler) GeneratePairCode(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	if !h.hubRef.Enabled() {
		response.Error(c, http.StatusServiceUnavailable, "im hub is not running")
		return
	}
	code, expiresAt, err := h.hubRef.GeneratePairCode(c.Request.Context(), id)
	if err != nil {
		mapErr(c, err)
		return
	}
	response.Success(c, gin.H{"code": code, "expires_at": expiresAt})
}

// ListChats GET /im-bots/:id/chats
func (h *IMBotHandler) ListChats(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	page, pageSize := pageParams(c)
	chats, pg, err := h.imBotService.ListChats(c.Request.Context(), id, page, pageSize)
	if err != nil {
		mapErr(c, err)
		return
	}
	response.Success(c, gin.H{"items": chats, "pagination": pg})
}

// UpdateChat PUT /im-bots/:id/chats/:chatId
func (h *IMBotHandler) UpdateChat(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	chatID, ok := parseIDParam(c, "chatId")
	if !ok {
		return
	}
	var req service.IMChatUpdateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request: "+err.Error())
		return
	}
	fields, err := req.ToFields()
	if err != nil {
		mapErr(c, err)
		return
	}
	if err := h.imBotService.UpdateChat(c.Request.Context(), id, chatID, fields); err != nil {
		mapErr(c, err)
		return
	}
	response.Success(c, gin.H{"message": "updated"})
}

// DeleteChat DELETE /im-bots/:id/chats/:chatId — unpair.
func (h *IMBotHandler) DeleteChat(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	chatID, ok := parseIDParam(c, "chatId")
	if !ok {
		return
	}
	if err := h.imBotService.DeleteChat(c.Request.Context(), id, chatID); err != nil {
		mapErr(c, err)
		return
	}
	response.Success(c, gin.H{"message": "unpaired"})
}

// ListChatMessages GET /im-bots/:id/chats/:chatId/messages
func (h *IMBotHandler) ListChatMessages(c *gin.Context) {
	chatID, ok := parseIDParam(c, "chatId")
	if !ok {
		return
	}
	page, pageSize := pageParams(c)
	msgs, pg, err := h.imBotService.ListChatMessages(c.Request.Context(), chatID, page, pageSize)
	if err != nil {
		mapErr(c, err)
		return
	}
	response.Success(c, gin.H{"items": msgs, "pagination": pg})
}

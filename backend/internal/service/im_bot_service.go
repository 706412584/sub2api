package service

import (
	"context"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

// CreateIMBotInput creates a bot (status starts disabled; enable separately).
type CreateIMBotInput struct {
	Name               string `json:"name" binding:"required"`
	Platform           string `json:"platform" binding:"required"`
	CredentialsJSON    string `json:"credentials" binding:"required"`
	APIKeyID           int64  `json:"api_key_id" binding:"required"`
	ModelOverride      string `json:"model_override"`
	SystemPrompt       string `json:"system_prompt"`
	MaxConcurrency     int    `json:"max_concurrency"`
	HistoryMaxMessages int    `json:"history_max_messages"`
	PairingEnabled     *bool  `json:"pairing_enabled"`
}

// UpdateIMBotInput patches a bot; nil/zero fields keep stored values.
// CredentialsJSON == nil keeps credentials; "" is invalid (use no field).
type UpdateIMBotInput struct {
	Name               *string `json:"name"`
	Platform           *string `json:"platform"`
	CredentialsJSON    *string `json:"credentials"`
	APIKeyID           *int64  `json:"api_key_id"`
	ModelOverride      *string `json:"model_override"`
	SystemPrompt       *string `json:"system_prompt"`
	MaxConcurrency     *int    `json:"max_concurrency"`
	HistoryMaxMessages *int    `json:"history_max_messages"`
	PairingEnabled     *bool   `json:"pairing_enabled"`
}

const (
	IMBotMaxConcurrencyDefault = 2
	IMBotHistoryMaxDefault     = 40
	IMBotMaxConcurrencyLimit   = 16
	IMBotHistoryMaxLimit       = 200
)

// IMBotService handles admin CRUD for IM bots and bridges to the running
// hub via IMBotReloader. Implemented as a standalone service (not part of
// the huge adminServiceImpl) to keep the im module's dependencies narrow.
type IMBotService struct {
	repo                    IMBotRepository
	apiKeyRepo              APIKeyRepository
	encryptor               SecretEncryptor
	reloader                IMBotReloader
	encryptionKeyConfigured bool
}

// NewIMBotService wires the service; reloader may be nil in unit tests.
func NewIMBotService(
	repo IMBotRepository,
	apiKeyRepo APIKeyRepository,
	encryptor SecretEncryptor,
	reloader IMBotReloader,
	encryptionKeyConfigured bool,
) *IMBotService {
	return &IMBotService{
		repo:                    repo,
		apiKeyRepo:              apiKeyRepo,
		encryptor:               encryptor,
		reloader:                reloader,
		encryptionKeyConfigured: encryptionKeyConfigured,
	}
}

func imBotBadRequest(reason, message string) error {
	return infraerrors.BadRequest(reason, message)
}

// validateKey ensures the bound key exists before wiring a bot to it.
func (s *IMBotService) validateKey(ctx context.Context, keyID int64) error {
	key, err := s.apiKeyRepo.GetByID(ctx, keyID)
	if err != nil || key == nil {
		return imBotBadRequest("IM_BOT_KEY_INVALID", "bound api key not found")
	}
	return nil
}

// Create stores a bot (disabled). The caller passes already-validated
// credential JSON (platform schema validation happens in the handler layer
// via the hub's factory registry).
func (s *IMBotService) Create(ctx context.Context, input *CreateIMBotInput) (*IMBot, error) {
	if input.Name == "" || input.Platform == "" {
		return nil, imBotBadRequest("IM_BOT_FIELDS_REQUIRED", "name and platform are required")
	}
	if !s.encryptionKeyConfigured {
		return nil, infraerrors.BadRequest("SECRET_ENCRYPTION_KEY_NOT_CONFIGURED",
			"cannot store the IM bot credentials: no fixed secret encryption key is configured, "+
				"so the auto-generated key would change on every restart and make credentials "+
				"undecryptable after a restart or upgrade. Set a fixed TOTP_ENCRYPTION_KEY "+
				"(e.g. generate one with `openssl rand -hex 32`) and try again")
	}
	if err := s.validateKey(ctx, input.APIKeyID); err != nil {
		return nil, err
	}

	enc, err := s.encryptor.Encrypt(input.CredentialsJSON)
	if err != nil {
		return nil, err
	}

	bot := &IMBot{
		Name:               input.Name,
		Platform:           input.Platform,
		CredentialsEnc:     enc,
		APIKeyID:           input.APIKeyID,
		ModelOverride:      input.ModelOverride,
		SystemPrompt:       input.SystemPrompt,
		Status:             IMBotStatusDisabled,
		MaxConcurrency:     normalizeInt(input.MaxConcurrency, IMBotMaxConcurrencyDefault, IMBotMaxConcurrencyLimit),
		HistoryMaxMessages: normalizeInt(input.HistoryMaxMessages, IMBotHistoryMaxDefault, IMBotHistoryMaxLimit),
		PairingEnabled:     input.PairingEnabled == nil || *input.PairingEnabled,
	}
	if err := s.repo.Create(ctx, bot); err != nil {
		return nil, err
	}
	return bot, nil
}

// Update patches a bot and hot-reloads the adapter when runtime-relevant
// fields changed.
func (s *IMBotService) Update(ctx context.Context, id int64, input *UpdateIMBotInput) (*IMBot, error) {
	bot, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	fields := IMBotUpdateFields{}
	if input.Name != nil && *input.Name != "" {
		fields.Name = input.Name
	}
	if input.Platform != nil && *input.Platform != "" {
		fields.Platform = input.Platform
	}
	if input.CredentialsJSON != nil {
		if !s.encryptionKeyConfigured {
			return nil, infraerrors.BadRequest("SECRET_ENCRYPTION_KEY_NOT_CONFIGURED",
				"cannot update IM bot credentials: no fixed secret encryption key is configured")
		}
		enc, err := s.encryptor.Encrypt(*input.CredentialsJSON)
		if err != nil {
			return nil, err
		}
		fields.CredentialsEnc = &enc
	}
	if input.APIKeyID != nil {
		if err := s.validateKey(ctx, *input.APIKeyID); err != nil {
			return nil, err
		}
		fields.APIKeyID = input.APIKeyID
	}
	if input.ModelOverride != nil {
		fields.ModelOverride = input.ModelOverride
	}
	if input.SystemPrompt != nil {
		fields.SystemPrompt = input.SystemPrompt
	}
	if input.MaxConcurrency != nil {
		v := normalizeInt(*input.MaxConcurrency, IMBotMaxConcurrencyDefault, IMBotMaxConcurrencyLimit)
		fields.MaxConcurrency = &v
	}
	if input.HistoryMaxMessages != nil {
		v := normalizeInt(*input.HistoryMaxMessages, IMBotHistoryMaxDefault, IMBotHistoryMaxLimit)
		fields.HistoryMaxMessages = &v
	}
	if input.PairingEnabled != nil {
		fields.PairingEnabled = input.PairingEnabled
	}

	if err := s.repo.Update(ctx, bot, fields); err != nil {
		return nil, err
	}
	if s.reloader != nil {
		if err := s.reloader.ReloadBot(ctx, id); err != nil {
			// CRUD succeeded; adapter reload failure is surfaced as status.
			_ = s.repo.UpdateStatus(ctx, id, IMBotStatusError, err.Error())
		}
	}
	return s.repo.GetByID(ctx, id)
}

// Delete stops the adapter and soft-deletes the bot.
func (s *IMBotService) Delete(ctx context.Context, id int64) error {
	if s.reloader != nil {
		_ = s.reloader.StopBot(ctx, id)
	}
	return s.repo.Delete(ctx, id)
}

// SetEnabled toggles a bot and starts/stops its adapter.
func (s *IMBotService) SetEnabled(ctx context.Context, id int64, enabled bool) (*IMBot, error) {
	if _, err := s.repo.GetByID(ctx, id); err != nil {
		return nil, err
	}
	status := IMBotStatusDisabled
	if enabled {
		status = IMBotStatusEnabled
	}
	if err := s.repo.UpdateStatus(ctx, id, status, ""); err != nil {
		return nil, err
	}
	if s.reloader != nil {
		if enabled {
			if err := s.reloader.ReloadBot(ctx, id); err != nil {
				_ = s.repo.UpdateStatus(ctx, id, IMBotStatusError, err.Error())
			}
		} else {
			_ = s.reloader.StopBot(ctx, id)
		}
	}
	return s.repo.GetByID(ctx, id)
}

// List lists bots with filters.
func (s *IMBotService) List(ctx context.Context, page, pageSize int, platform, status, search, sortBy, sortOrder string) ([]IMBot, *pagination.PaginationResult, error) {
	params := pagination.PaginationParams{
		Page: page, PageSize: pageSize, SortBy: sortBy, SortOrder: sortOrder,
	}
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize < 1 || params.PageSize > 100 {
		params.PageSize = 20
	}
	return s.repo.ListWithFilters(ctx, params, platform, status, search)
}

// GetByID fetches one bot.
func (s *IMBotService) GetByID(ctx context.Context, id int64) (*IMBot, error) {
	return s.repo.GetByID(ctx, id)
}

// ListChats pages a bot's paired chats.
func (s *IMBotService) ListChats(ctx context.Context, botID int64, page, pageSize int) ([]IMBotChat, *pagination.PaginationResult, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize < 1 || params.PageSize > 100 {
		params.PageSize = 20
	}
	return s.repo.ListChats(ctx, botID, params)
}

// UpdateChat blocks/unblocks or re-models a chat.
func (s *IMBotService) UpdateChat(ctx context.Context, botID, chatID int64, fields IMChatUpdateFields) error {
	chat, err := s.repo.GetChatByID(ctx, chatID)
	if err != nil {
		return err
	}
	if chat.BotID != botID {
		return imBotBadRequest("IM_CHAT_BOT_MISMATCH", "chat does not belong to this bot")
	}
	if fields.Status != nil && *fields.Status != IMChatStatusActive && *fields.Status != IMChatStatusBlocked {
		return imBotBadRequest("IM_CHAT_STATUS_INVALID", "status must be active or blocked")
	}
	return s.repo.UpdateChat(ctx, chatID, fields)
}

// DeleteChat unpairs a chat (user must re-pair with a fresh code).
func (s *IMBotService) DeleteChat(ctx context.Context, botID, chatID int64) error {
	chat, err := s.repo.GetChatByID(ctx, chatID)
	if err != nil {
		return err
	}
	if chat.BotID != botID {
		return imBotBadRequest("IM_CHAT_BOT_MISMATCH", "chat does not belong to this bot")
	}
	return s.repo.DeleteChat(ctx, chatID)
}

// ListChatMessages pages a chat's history (admin read-only view).
func (s *IMBotService) ListChatMessages(ctx context.Context, chatID int64, page, pageSize int) ([]IMBotMessage, *pagination.PaginationResult, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize < 1 || params.PageSize > 100 {
		params.PageSize = 50
	}
	return s.repo.ListMessagesPage(ctx, chatID, params)
}

// RuntimeStatus proxies the hub view.
func (s *IMBotService) RuntimeStatus(ctx context.Context, botID int64) (*IMBotRuntimeStatus, error) {
	if s.reloader == nil {
		return nil, infraerrors.ServiceUnavailable("IM_HUB_UNAVAILABLE", "im hub is not running")
	}
	return s.reloader.RuntimeStatus(ctx, botID)
}

func normalizeInt(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// IMChatUpdateInput is the admin request body for PUT /im-bots/:id/chats/:chatId.
type IMChatUpdateInput struct {
	Status        *string `json:"status"`
	ModelOverride *string `json:"model_override"`
	DisplayName   *string `json:"display_name"`
}

// ToFields validates and converts to repo fields.
func (i *IMChatUpdateInput) ToFields() (IMChatUpdateFields, error) {
	fields := IMChatUpdateFields{
		Status:        i.Status,
		ModelOverride: i.ModelOverride,
		DisplayName:   i.DisplayName,
	}
	if i.Status != nil && *i.Status != IMChatStatusActive && *i.Status != IMChatStatusBlocked {
		return fields, infraerrors.BadRequest("IM_CHAT_STATUS_INVALID", "status must be active or blocked")
	}
	return fields, nil
}

// IMPlatformSchema describes one platform for the admin wizard (list of
// credential fields, availability).
type IMPlatformSchema struct {
	Platform   string                      `json:"platform"`
	Available  bool                        `json:"available"`
	Credential []IMPlatformCredentialField `json:"credential_fields"`
}

// IMPlatformCredentialField is the service-side mirror of imapi.CredentialField.
type IMPlatformCredentialField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Secret      bool   `json:"secret"`
	Placeholder string `json:"placeholder,omitempty"`
	Required    bool   `json:"required"`
}

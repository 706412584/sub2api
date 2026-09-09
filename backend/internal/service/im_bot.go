package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	// ErrIMBotNotFound is returned when a bot id does not exist.
	ErrIMBotNotFound = infraerrors.NotFound("IM_BOT_NOT_FOUND", "im bot not found")
	// ErrIMChatNotFound is returned when a paired chat does not exist.
	ErrIMChatNotFound = infraerrors.NotFound("IM_CHAT_NOT_FOUND", "im chat not found")
	// ErrIMPlatformUnsupported is returned for platforms without a registered adapter factory.
	ErrIMPlatformUnsupported = infraerrors.BadRequest("IM_PLATFORM_UNSUPPORTED", "im platform has no adapter registered")
)

// IM bot / chat status values.
const (
	IMBotStatusEnabled  = "enabled"
	IMBotStatusDisabled = "disabled"
	IMBotStatusError    = "error"

	IMChatStatusActive  = "active"
	IMChatStatusBlocked = "blocked"

	IMMessageRoleUser      = "user"
	IMMessageRoleAssistant = "assistant"
)

// IMBot is an IM platform bot bound to an existing API key. Messages from
// paired private chats are forwarded through the local gateway using that
// key, so auth/group/allowlist/billing all ride the existing pipeline.
type IMBot struct {
	ID                 int64     `json:"id"`
	Name               string    `json:"name"`
	Platform           string    `json:"platform"`
	CredentialsEnc     string    `json:"-"`
	APIKeyID           int64     `json:"api_key_id"`
	ModelOverride      string    `json:"model_override"`
	SystemPrompt       string    `json:"system_prompt"`
	Status             string    `json:"status"`
	MaxConcurrency     int       `json:"max_concurrency"`
	HistoryMaxMessages int       `json:"history_max_messages"`
	PairingEnabled     bool      `json:"pairing_enabled"`
	LastError          string    `json:"last_error"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// IMBotChat is one paired chat window. SessionUUID is stable across turns
// (sticky scheduling + usage-log correlation) and rotated on /new.
type IMBotChat struct {
	ID             int64      `json:"id"`
	BotID          int64      `json:"bot_id"`
	ChatID         string     `json:"chat_id"`
	PlatformUserID string     `json:"platform_user_id"`
	DisplayName    string     `json:"display_name"`
	SessionUUID    string     `json:"session_uuid"`
	ModelOverride  string     `json:"model_override"`
	Status         string     `json:"status"`
	PairedAt       time.Time  `json:"paired_at"`
	LastMessageAt  *time.Time `json:"last_message_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// IMBotMessage is one stored chat turn used to rebuild multi-turn context.
type IMBotMessage struct {
	ID        int64     `json:"id"`
	ChatID    int64     `json:"chat_id"`
	BotID     int64     `json:"bot_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	RequestID string    `json:"request_id"`
	CreatedAt time.Time `json:"created_at"`
}

// IMBotUpdateFields selects which columns an Update call touches; nil fields
// keep the stored value.
type IMBotUpdateFields struct {
	Name               *string
	Platform           *string
	CredentialsEnc     *string
	APIKeyID           *int64
	ModelOverride      *string
	SystemPrompt       *string
	Status             *string
	MaxConcurrency     *int
	HistoryMaxMessages *int
	PairingEnabled     *bool
	LastError          *string
}

// IMBotRepository persists IM bots, their paired chats and chat history.
type IMBotRepository interface {
	// Bots
	Create(ctx context.Context, bot *IMBot) error
	GetByID(ctx context.Context, id int64) (*IMBot, error)
	ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string) ([]IMBot, *pagination.PaginationResult, error)
	ListEnabled(ctx context.Context) ([]IMBot, error)
	Update(ctx context.Context, bot *IMBot, fields IMBotUpdateFields) error
	Delete(ctx context.Context, id int64) error
	UpdateStatus(ctx context.Context, id int64, status, lastError string) error

	// Chats
	GetChatByID(ctx context.Context, id int64) (*IMBotChat, error)
	GetChatByBotAndChatID(ctx context.Context, botID int64, chatID string) (*IMBotChat, error)
	UpsertChat(ctx context.Context, chat *IMBotChat) error
	RotateChatSessionUUID(ctx context.Context, chatID int64) (string, error)
	ListChats(ctx context.Context, botID int64, params pagination.PaginationParams) ([]IMBotChat, *pagination.PaginationResult, error)
	UpdateChat(ctx context.Context, chatID int64, fields IMChatUpdateFields) error
	DeleteChat(ctx context.Context, chatID int64) error

	// Messages
	AppendMessage(ctx context.Context, m *IMBotMessage) error
	ListRecentMessages(ctx context.Context, chatID int64, limit int) ([]IMBotMessage, error)
	DeleteChatMessages(ctx context.Context, chatID int64) error
	ListMessagesPage(ctx context.Context, chatID int64, params pagination.PaginationParams) ([]IMBotMessage, *pagination.PaginationResult, error)
	SweepExpiredMessages(ctx context.Context, before time.Time) (int64, error)
}

// IMChatUpdateFields selects chat columns to touch on UpdateChat.
type IMChatUpdateFields struct {
	Status        *string
	ModelOverride *string
	DisplayName   *string
}

// IMBotReloader bridges admin CRUD to the running IM hub without the service
// package importing the im module (payment-module pattern to avoid cycles).
// Implemented by im.Hub, injected via wire.
type IMBotReloader interface {
	// ReloadBot stops and re-creates the adapter after a create/update/enable.
	ReloadBot(ctx context.Context, botID int64) error
	// StopBot tears the adapter down after disable/delete.
	StopBot(ctx context.Context, botID int64) error
	// RuntimeStatus reports whether the adapter loop is alive.
	RuntimeStatus(ctx context.Context, botID int64) (*IMBotRuntimeStatus, error)
}

// IMBotRuntimeStatus is the hub-side view of one bot's adapter loop.
type IMBotRuntimeStatus struct {
	BotID          int64      `json:"bot_id"`
	Running        bool       `json:"running"`
	IsLeader       bool       `json:"is_leader"`
	LastError      string     `json:"last_error"`
	ConnectedSince *time.Time `json:"connected_since"`
}

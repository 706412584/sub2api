// Package imapi defines the platform-neutral IM adapter contracts. Both
// internal/im (the hub) and internal/im/platform/* (adapters) depend on it;
// it depends on nothing inside the app, so no import cycles can form.
package imapi

import (
	"context"
	"time"
)

// Platform identifies an IM platform.
type Platform string

const (
	PlatformTelegram Platform = "telegram"
	PlatformFeishu   Platform = "feishu"
	PlatformDingTalk Platform = "dingtalk"
	PlatformWeCom    Platform = "wecom"
	PlatformQQ       Platform = "qq"
	PlatformSlack    Platform = "slack"
	PlatformWeChat   Platform = "wechat"
	PlatformWhatsApp Platform = "whatsapp"
)

// AllPlatforms is every platform the schema layer knows about.
var AllPlatforms = []Platform{
	PlatformTelegram, PlatformFeishu, PlatformDingTalk, PlatformWeCom,
	PlatformQQ, PlatformSlack, PlatformWeChat, PlatformWhatsApp,
}

// String satisfies fmt.Stringer.
func (p Platform) String() string { return string(p) }

// InboundMessage is a normalized private-chat message produced by a platform
// adapter. Group messages are dropped by the adapter itself.
type InboundMessage struct {
	BotID        int64
	ChatID       string // platform-native chat identifier
	PlatformUser string // pairing/rate-limit dimension
	DisplayName  string
	RawMessageID string // dedup key
	Text         string
	ReceivedAt   time.Time
}

// OutboundStream carries one assistant turn's incremental text back to the
// platform adapter.
type OutboundStream interface {
	// Append delivers one delta. Implementations batch internally.
	Append(delta string)
	// Finish closes the turn; err is the upstream termination cause (nil = ok).
	Finish(err error)
}

// ChatPort is the outbound capability set a platform adapter provides.
type ChatPort interface {
	// SendNotice posts a non-streamed notice (pairing prompt, errors, command output).
	SendNotice(ctx context.Context, chatID, text string) error
	// CreateStream opens the rendering channel for one assistant turn.
	CreateStream(ctx context.Context, chatID string) OutboundStream
}

// Optional extension reserved for the tool phase (image replies). Detect with
// a type assertion; platforms that cannot send images simply don't implement it.
type ImageSender interface {
	SendImage(ctx context.Context, chatID, url, caption string) error
}

// PlatformAdapter runs one bot's platform connection. Each implementation
// owns its SDK and event loop (run inside its own goroutine by the hub).
type PlatformAdapter interface {
	// Run blocks on the platform event loop until ctx is cancelled or a
	// fatal error occurs. Every private-chat message is handed to handler.
	Run(ctx context.Context, handler func(ctx context.Context, msg InboundMessage)) error
	// TestConnection validates the credentials (admin wizard, ~10s budget).
	TestConnection(ctx context.Context) error
	// ChatPort returns the outbound port (valid once Run has started).
	ChatPort() ChatPort
}

// CredentialField describes one credential input for the admin wizard's
// dynamic form.
type CredentialField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Secret      bool   `json:"secret"`
	Placeholder string `json:"placeholder,omitempty"`
	Required    bool   `json:"required"`
}

// BotConfig is the hub-side view of a bot row (no credential plaintext).
type BotConfig struct {
	ID                 int64
	Name               string
	Platform           Platform
	ModelOverride      string
	SystemPrompt       string
	MaxConcurrency     int
	HistoryMaxMessages int
}

// AdapterFactory builds adapters for one platform.
type AdapterFactory interface {
	Platform() Platform
	// Create builds an adapter from decrypted credential JSON.
	Create(bot BotConfig, creds []byte) (PlatformAdapter, error)
	// CredentialSchema returns the wizard's form fields.
	CredentialSchema() []CredentialField
}

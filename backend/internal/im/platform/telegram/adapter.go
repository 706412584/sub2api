// Package telegram implements the IM adapter for Telegram Bot API long
// polling. Streaming uses editMessageText as a typewriter with seal-and-continue
// chunking (TELEGRAM_STREAM_MAX bytes per message), following the parameter
// set validated by cc-haha.
package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/Wei-Shaw/sub2api/internal/imapi"
	"github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	// Telegram hard caps a message at 4096 characters; keep headroom for
	// multi-byte rendering (cc-haha uses 3998).
	streamMaxChars = 3998
	// Batch window for the typewriter (editMessageText frequency + 429 safety).
	flushInterval = 500 * time.Millisecond
	flushChars    = 200
)

// Credentials is the decrypted credential JSON shape.
type Credentials struct {
	BotToken string `json:"bot_token"`
}

// Factory builds telegram adapters.
type Factory struct{}

// NewFactory constructs the telegram adapter factory.
func NewFactory() *Factory { return &Factory{} }

// Platform implements imapi.AdapterFactory.
func (f *Factory) Platform() imapi.Platform { return imapi.PlatformTelegram }

// CredentialSchema implements imapi.AdapterFactory.
func (f *Factory) CredentialSchema() []imapi.CredentialField {
	return []imapi.CredentialField{
		{Key: "bot_token", Label: "Bot Token", Secret: true, Required: true, Placeholder: "123456:ABC-DEF..."},
	}
}

// ValidateCredentials implements imapi.AdapterFactory.
func (f *Factory) ValidateCredentials(raw []byte) error {
	var c Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return errors.BadRequest("IM_CREDENTIALS_INVALID", "credentials must be JSON: "+err.Error())
	}
	if strings.TrimSpace(c.BotToken) == "" {
		return errors.BadRequest("IM_CREDENTIALS_INVALID", "bot_token is required")
	}
	return nil
}

// Create implements imapi.AdapterFactory.
func (f *Factory) Create(_ imapi.BotConfig, creds []byte) (imapi.PlatformAdapter, error) {
	if err := f.ValidateCredentials(creds); err != nil {
		return nil, err
	}
	var c Credentials
	_ = json.Unmarshal(creds, &c)
	return &Adapter{botToken: c.BotToken}, nil
}

// Adapter is the telegram platform adapter.
type Adapter struct {
	botToken string

	bot     *tgbotapi.BotAPI
	port    *Port
	started bool

	// cached @username from getMe (stable for a bot's lifetime)
	username      string
	usernameReady bool
	mu            sync.Mutex
}

// Run implements imapi.PlatformAdapter: long-poll loop, private chats only.
func (a *Adapter) Run(ctx context.Context, handler func(ctx context.Context, msg imapi.InboundMessage)) error {
	bot, err := tgbotapi.NewBotAPIWithClient(a.botToken, tgbotapi.APIEndpoint, httpClient())
	if err != nil {
		return fmt.Errorf("telegram auth failed: %w", err)
	}
	a.mu.Lock()
	a.bot = bot
	a.port = &Port{bot: bot}
	a.started = true
	if me, err := bot.GetMe(); err == nil && me.UserName != "" {
		a.username = me.UserName
		a.usernameReady = true
	}
	a.mu.Unlock()

	cfg := tgbotapi.NewUpdate(0)
	cfg.Timeout = 30 //nolint:mnd // long poll window
	updates := bot.GetUpdatesChan(cfg)

	for {
		select {
		case <-ctx.Done():
			return nil
		case u := <-updates:
			// Private chats only; group/channel messages are ignored.
			if u.Message == nil || u.Message.Chat == nil || u.Message.Chat.Type != "private" {
				continue
			}
			text := strings.TrimSpace(u.Message.Text)
			if text == "" {
				continue
			}
			display := strings.TrimSpace(u.Message.From.FirstName + " " + u.Message.From.LastName)
			handler(ctx, imapi.InboundMessage{
				ChatID:       fmt.Sprintf("%d", u.Message.Chat.ID),
				PlatformUser: fmt.Sprintf("%d", u.Message.From.ID),
				DisplayName:  display,
				RawMessageID: fmt.Sprintf("%d", u.Message.MessageID),
				Text:         text,
				ReceivedAt:   time.Now(),
			})
		}
	}
}

// TestConnection implements imapi.PlatformAdapter: call getMe.
func (a *Adapter) TestConnection(ctx context.Context) error {
	bot, err := tgbotapi.NewBotAPIWithClient(a.botToken, tgbotapi.APIEndpoint, httpClient())
	if err != nil {
		return fmt.Errorf("telegram auth failed: %w", err)
	}
	me, err := bot.GetMe()
	if err != nil {
		return fmt.Errorf("telegram getMe failed: %w", err)
	}
	_ = ctx
	if me.UserName == "" {
		return errors.New(http.StatusBadGateway, "IM_TEST_EMPTY", "telegram returned an empty bot username")
	}
	return nil
}

// BotUsername implements imapi.ProfileProvider: the cached @username (also
// resolves on demand when the adapter isn't running yet).
func (a *Adapter) BotUsername(ctx context.Context) (string, error) {
	a.mu.Lock()
	cached, ready, token, bot := a.username, a.usernameReady, a.botToken, a.bot
	a.mu.Unlock()
	if ready {
		return cached, nil
	}
	if bot == nil {
		var err error
		bot, err = tgbotapi.NewBotAPIWithClient(token, tgbotapi.APIEndpoint, httpClient())
		if err != nil {
			return "", fmt.Errorf("telegram auth failed: %w", err)
		}
	}
	me, err := bot.GetMe()
	if err != nil {
		return "", fmt.Errorf("telegram getMe failed: %w", err)
	}
	_ = ctx
	if me.UserName == "" {
		return "", errors.New(http.StatusBadGateway, "IM_TEST_EMPTY", "telegram returned an empty bot username")
	}
	a.mu.Lock()
	a.username, a.usernameReady = me.UserName, true
	a.mu.Unlock()
	return me.UserName, nil
}

// ChatPort implements imapi.PlatformAdapter.
func (a *Adapter) ChatPort() imapi.ChatPort {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.port
}

// httpClient builds a TG API client with sane timeouts.
func httpClient() *http.Client {
	return &http.Client{Timeout: 65 * time.Second} //nolint:mnd // long poll + margin
}

package im

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/config"
	feishu "github.com/Wei-Shaw/sub2api/internal/im/platform/feishu"
	telegram "github.com/Wei-Shaw/sub2api/internal/im/platform/telegram"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
)

// HubRef is the consumer-facing handle handed to handlers (nil-safe ops).
type HubRef struct {
	hub *Hub
}

// Enabled reports whether the IM subsystem is on.
func (r HubRef) Enabled() bool { return r.hub != nil }

// GeneratePairCode issues a pairing code for a bot.
func (r HubRef) GeneratePairCode(ctx context.Context, botID int64) (string, any, error) {
	if r.hub == nil {
		return "", nil, service.ErrIMBotNotFound
	}
	code, expiresAt, err := r.hub.GeneratePairCode(ctx, botID)
	return code, expiresAt, err
}

// TestConnection validates stored credentials for a bot.
func (r HubRef) TestConnection(ctx context.Context, bot *service.IMBot) error {
	if r.hub == nil {
		return service.ErrIMPlatformUnsupported
	}
	factory := r.hub.factories[Platform(bot.Platform)]
	if factory == nil {
		return fmt.Errorf("%w: %s", service.ErrIMPlatformUnsupported, bot.Platform)
	}
	creds, err := r.hub.enc.Decrypt(bot.CredentialsEnc)
	if err != nil {
		return err
	}
	adapter, err := factory.Create(BotConfig{
		ID:                 bot.ID,
		Name:               bot.Name,
		Platform:           Platform(bot.Platform),
		ModelOverride:      bot.ModelOverride,
		SystemPrompt:       bot.SystemPrompt,
		MaxConcurrency:     bot.MaxConcurrency,
		HistoryMaxMessages: bot.HistoryMaxMessages,
	}, []byte(creds))
	if err != nil {
		return err
	}
	testCtx, cancel := context.WithTimeout(ctx, 10_000_000_000) //nolint:mnd // 10s
	defer cancel()
	return adapter.TestConnection(testCtx)
}

// PlatformSchemas lists every registered platform's credential schema for
// the admin wizard's dynamic forms (service-side mirror type).
func (r HubRef) PlatformSchemas() []service.IMPlatformSchema {
	if r.hub == nil {
		return nil
	}
	out := make([]service.IMPlatformSchema, 0, len(r.hub.factories))
	registered := make(map[Platform]bool, len(r.hub.factories))
	for _, f := range r.hub.factories {
		creds := make([]service.IMPlatformCredentialField, 0, len(f.CredentialSchema()))
		for _, cf := range f.CredentialSchema() {
			creds = append(creds, service.IMPlatformCredentialField{
				Key: cf.Key, Label: cf.Label, Secret: cf.Secret,
				Placeholder: cf.Placeholder, Required: cf.Required,
			})
		}
		out = append(out, service.IMPlatformSchema{
			Platform: string(f.Platform()), Available: true, Credential: creds,
		})
		registered[f.Platform()] = true
	}
	for _, p := range AllPlatforms {
		if !registered[p] {
			out = append(out, service.IMPlatformSchema{Platform: string(p), Available: false})
		}
	}
	return out
}

// Factories lists every registered platform's credential schema for the
// admin wizard's dynamic forms.
func (r HubRef) Factories() []AdapterFactory {
	if r.hub == nil {
		return nil
	}
	out := make([]AdapterFactory, 0, len(r.hub.factories))
	for _, p := range AllPlatforms {
		if f, ok := r.hub.factories[p]; ok {
			out = append(out, f)
		}
	}
	return out
}

// ProvideIMHub builds the singleton hub and starts it (leader election +
// enabled bots). Registration into cleanup is done by the caller (cmd/server
// wire) — Stop must run at shutdown.
func ProvideIMHub(
	cfg *config.Config,
	botRepo service.IMBotRepository,
	apiKeyRepo service.APIKeyRepository,
	encryptor service.SecretEncryptor,
	rdb *redis.Client,
) *Hub {
	imCfg := cfg.IM
	if imCfg.BaseURL == "" {
		imCfg.BaseURL = fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.Port)
	}
	hub := NewHub(imCfg, botRepo, apiKeyRepo, encryptor, rdb, []AdapterFactory{telegram.NewFactory(), feishu.NewFactory()})
	_ = hub.Start(context.Background())
	return hub
}

// ProvideHubRef wraps the hub for handler injection.
func ProvideHubRef(hub *Hub) HubRef {
	return HubRef{hub: hub}
}

// ProvideIMBotService wires the admin CRUD service with the running hub.
func ProvideIMBotService(
	repo service.IMBotRepository,
	apiKeyRepo service.APIKeyRepository,
	encryptor service.SecretEncryptor,
	hub *Hub,
	cfg *config.Config,
) *service.IMBotService {
	return service.NewIMBotService(repo, apiKeyRepo, encryptor, hub, cfg.Totp.EncryptionKeyConfigured)
}

// ProviderSet is the wire provider set for the IM module. IMBotService is
// constructed here (not in service.ProviderSet) because it needs the Hub as
// its IMBotReloader — an im package type that service cannot import.
var ProviderSet = wire.NewSet(
	ProvideIMHub,
	ProvideIMBotService,
	ProvideHubRef,
)

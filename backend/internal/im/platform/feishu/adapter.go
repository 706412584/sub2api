// Package feishu registers the Feishu (Lark) adapter factory. Phase 1
// scaffolding: the full WS long-connection + CardKit streaming adapter
// lands in the next iteration; the factory already exposes the credential
// schema so the admin wizard can render its form.
package feishu

import (
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/imapi"
	"github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Credentials is the decrypted credential JSON shape.
type Credentials struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
}

// Factory builds feishu adapters.
type Factory struct{}

// NewFactory constructs the feishu adapter factory.
func NewFactory() *Factory { return &Factory{} }

// Platform implements imapi.AdapterFactory.
func (f *Factory) Platform() imapi.Platform { return imapi.PlatformFeishu }

// CredentialSchema implements imapi.AdapterFactory.
func (f *Factory) CredentialSchema() []imapi.CredentialField {
	return []imapi.CredentialField{
		{Key: "app_id", Label: "App ID", Required: true, Placeholder: "cli_xxx"},
		{Key: "app_secret", Label: "App Secret", Secret: true, Required: true},
	}
}

// ValidateCredentials implements imapi.AdapterFactory.
func (f *Factory) ValidateCredentials(raw []byte) error {
	var c Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return errors.BadRequest("IM_CREDENTIALS_INVALID", "credentials must be JSON: "+err.Error())
	}
	if strings.TrimSpace(c.AppID) == "" || strings.TrimSpace(c.AppSecret) == "" {
		return errors.BadRequest("IM_CREDENTIALS_INVALID", "app_id and app_secret are required")
	}
	return nil
}

// Create implements imapi.AdapterFactory. The streaming adapter is not
// implemented yet; construction fails with a clear reason.
func (f *Factory) Create(_ imapi.BotConfig, creds []byte) (imapi.PlatformAdapter, error) {
	if err := f.ValidateCredentials(creds); err != nil {
		return nil, err
	}
	return nil, errors.BadRequest("IM_PLATFORM_NOT_READY", "feishu adapter is not implemented yet (Phase 1 continues)")
}

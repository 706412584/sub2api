// Package im implements built-in IM chat bots: paired private chats on
// Telegram/Feishu/... platforms forwarded through the local gateway
// (/v1/messages) using a bot-bound API key.
//
// Layering (mirrors the payment module to avoid import cycles):
//
//	platform/<name>  -> implements imapi.PlatformAdapter + imapi.ChatPort (SDK isolated)
//	im.Hub           -> bot lifecycle, per-chat queues, pairing gate, commands
//	service          -> admin CRUD, depends only on IMBotRepository +
//	                    IMBotReloader (narrow interface, implemented by Hub)
//
// ChatPort design follows cc-haha's adapter layer (MIT): the runtime never
// imports a platform SDK, platforms only say how to say something.
package im

import (
	"context"
	"encoding/json"
	"fmt"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/Wei-Shaw/sub2api/internal/imapi"
)

// Contract aliases: the hub code refers to the shared imapi types.
type (
	Platform        = imapi.Platform
	InboundMessage  = imapi.InboundMessage
	OutboundStream  = imapi.OutboundStream
	ChatPort        = imapi.ChatPort
	ImageSender     = imapi.ImageSender
	PlatformAdapter = imapi.PlatformAdapter
	CredentialField = imapi.CredentialField
	BotConfig       = imapi.BotConfig
	AdapterFactory  = imapi.AdapterFactory
)

// Platform constants re-exported for hub-side switch statements.
const (
	PlatformTelegram = imapi.PlatformTelegram
	PlatformFeishu   = imapi.PlatformFeishu
	PlatformDingTalk = imapi.PlatformDingTalk
	PlatformWeCom    = imapi.PlatformWeCom
	PlatformQQ       = imapi.PlatformQQ
	PlatformSlack    = imapi.PlatformSlack
	PlatformWeChat   = imapi.PlatformWeChat
	PlatformWhatsApp = imapi.PlatformWhatsApp
)

// AllPlatforms is every platform the schema layer knows about.
var AllPlatforms = imapi.AllPlatforms

// VerifyCredentials checks the raw credential JSON before encryption/storage.
func VerifyCredentials(f AdapterFactory, raw []byte) error {
	if len(raw) == 0 {
		return infraerrors.BadRequest("IM_CREDENTIALS_REQUIRED", "credentials are required")
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return infraerrors.BadRequest("IM_CREDENTIALS_INVALID", "credentials must be a JSON object")
	}
	for _, f := range f.CredentialSchema() {
		if !f.Required {
			continue
		}
		v, ok := probe[f.Key]
		if !ok || v == nil {
			return infraerrors.BadRequest("IM_CREDENTIALS_INVALID", "credential field \""+f.Key+"\" is required")
		}
		if s, isStr := v.(string); isStr && s == "" {
			return infraerrors.BadRequest("IM_CREDENTIALS_INVALID", "credential field \""+f.Key+"\" must not be empty")
		}
	}
	return nil
}

// ModelFor returns the effective model for a chat: chat override > bot
// override > the bound key's group default (empty string means group default).
func ModelFor(chatModel, botModel string) string {
	if chatModel != "" {
		return chatModel
	}
	return botModel
}

// SessionUUIDKey builds the usage-log correlation header value.
func SessionUUIDKey(botID int64, sessionUUID string) string {
	return fmt.Sprintf("im:%d:%s", botID, sessionUUID)
}

// BuildMetadataUserID mirrors service.FormatMetadataUserID's legacy format
// (device 64-hex + empty account + session uuid) so GenerateSessionHash
// resolves a deterministic sticky session key per chat.
func BuildMetadataUserID(deviceHex64, sessionUUID string) string {
	return service.FormatMetadataUserID(deviceHex64, "", sessionUUID, "")
}

// keep context imported for future helpers.
var _ = context.Background

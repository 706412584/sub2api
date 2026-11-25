package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/imbot"
	"github.com/Wei-Shaw/sub2api/ent/imbotchat"
	"github.com/Wei-Shaw/sub2api/ent/imbotmessage"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type imBotRepository struct {
	client *dbent.Client
}

// newIMSessionUUID generates a random UUIDv4 (36-char lowercase form).
func newIMSessionUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

// NewIMBotRepository returns the IM bot repository backed by ent.
func NewIMBotRepository(client *dbent.Client) service.IMBotRepository {
	return &imBotRepository{client: client}
}

// ---------- bots ----------

func (r *imBotRepository) Create(ctx context.Context, bot *service.IMBot) error {
	created, err := r.client.IMBot.Create().
		SetName(bot.Name).
		SetPlatform(bot.Platform).
		SetCredentialsEncrypted(bot.CredentialsEnc).
		SetAPIKeyID(bot.APIKeyID).
		SetModelOverride(bot.ModelOverride).
		SetSystemPrompt(bot.SystemPrompt).
		SetStatus(bot.Status).
		SetMaxConcurrency(bot.MaxConcurrency).
		SetHistoryMaxMessages(bot.HistoryMaxMessages).
		SetPairingEnabled(bot.PairingEnabled).
		SetLastError(bot.LastError).
		Save(ctx)
	if err != nil {
		return err
	}
	applyIMBotEntity(bot, created)
	return nil
}

func (r *imBotRepository) GetByID(ctx context.Context, id int64) (*service.IMBot, error) {
	m, err := r.client.IMBot.Get(ctx, id)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrIMBotNotFound
		}
		return nil, err
	}
	return imBotEntityToService(m), nil
}

func (r *imBotRepository) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string) ([]service.IMBot, *pagination.PaginationResult, error) {
	q := r.client.IMBot.Query()
	if platform != "" {
		q = q.Where(imbot.PlatformEQ(platform))
	}
	if status != "" {
		q = q.Where(imbot.StatusEQ(status))
	}
	if search != "" {
		q = q.Where(imbot.NameContainsFold(search))
	}

	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	bots, err := q.
		Order(dbent.Desc(imbot.FieldCreatedAt)).
		Offset(params.Offset()).
		Limit(params.Limit()).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}

	out := make([]service.IMBot, 0, len(bots))
	for i := range bots {
		out = append(out, *imBotEntityToService(bots[i]))
	}
	return out, paginationResultFromTotal(int64(total), params), nil
}

func (r *imBotRepository) ListEnabled(ctx context.Context) ([]service.IMBot, error) {
	bots, err := r.client.IMBot.Query().
		Where(imbot.StatusEQ(service.IMBotStatusEnabled)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.IMBot, 0, len(bots))
	for i := range bots {
		out = append(out, *imBotEntityToService(bots[i]))
	}
	return out, nil
}

func (r *imBotRepository) Update(ctx context.Context, bot *service.IMBot, fields service.IMBotUpdateFields) error {
	builder := r.client.IMBot.UpdateOneID(bot.ID)
	if fields.Name != nil {
		builder = builder.SetName(*fields.Name)
	}
	if fields.Platform != nil {
		builder = builder.SetPlatform(*fields.Platform)
	}
	if fields.CredentialsEnc != nil {
		builder = builder.SetCredentialsEncrypted(*fields.CredentialsEnc)
	}
	if fields.APIKeyID != nil {
		builder = builder.SetAPIKeyID(*fields.APIKeyID)
	}
	if fields.ModelOverride != nil {
		builder = builder.SetModelOverride(*fields.ModelOverride)
	}
	if fields.SystemPrompt != nil {
		builder = builder.SetSystemPrompt(*fields.SystemPrompt)
	}
	if fields.Status != nil {
		builder = builder.SetStatus(*fields.Status)
	}
	if fields.MaxConcurrency != nil {
		builder = builder.SetMaxConcurrency(*fields.MaxConcurrency)
	}
	if fields.HistoryMaxMessages != nil {
		builder = builder.SetHistoryMaxMessages(*fields.HistoryMaxMessages)
	}
	if fields.PairingEnabled != nil {
		builder = builder.SetPairingEnabled(*fields.PairingEnabled)
	}
	if fields.LastError != nil {
		builder = builder.SetLastError(*fields.LastError)
	}
	updated, err := builder.Save(ctx)
	if err != nil {
		return err
	}
	applyIMBotEntity(bot, updated)
	return nil
}

func (r *imBotRepository) Delete(ctx context.Context, id int64) error {
	err := r.client.IMBot.DeleteOneID(id).Exec(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return service.ErrIMBotNotFound
		}
		return err
	}
	return nil
}

func (r *imBotRepository) UpdateStatus(ctx context.Context, id int64, status, lastError string) error {
	return r.client.IMBot.UpdateOneID(id).
		SetStatus(status).
		SetLastError(lastError).
		Exec(ctx)
}

// ---------- chats ----------

func (r *imBotRepository) GetChatByID(ctx context.Context, id int64) (*service.IMBotChat, error) {
	m, err := r.client.IMBotChat.Get(ctx, id)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrIMChatNotFound
		}
		return nil, err
	}
	return imChatEntityToService(m), nil
}

func (r *imBotRepository) GetChatByBotAndChatID(ctx context.Context, botID int64, chatID string) (*service.IMBotChat, error) {
	m, err := r.client.IMBotChat.Query().
		Where(
			imbotchat.BotID(botID),
			imbotchat.ChatID(chatID),
		).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrIMChatNotFound
		}
		return nil, err
	}
	return imChatEntityToService(m), nil
}

func (r *imBotRepository) UpsertChat(ctx context.Context, chat *service.IMBotChat) error {
	existing, err := r.client.IMBotChat.Query().
		Where(
			imbotchat.BotID(chat.BotID),
			imbotchat.ChatID(chat.ChatID),
		).
		Only(ctx)
	if err != nil && !dbent.IsNotFound(err) {
		return err
	}
	if existing != nil {
		updated, err := existing.Update().
			SetPlatformUserID(chat.PlatformUserID).
			SetDisplayName(chat.DisplayName).
			SetStatus(chat.Status).
			SetSessionUUID(chat.SessionUUID).
			Save(ctx)
		if err != nil {
			return err
		}
		applyIMChatEntity(chat, updated)
		return nil
	}
	created, err := r.client.IMBotChat.Create().
		SetBotID(chat.BotID).
		SetChatID(chat.ChatID).
		SetPlatformUserID(chat.PlatformUserID).
		SetDisplayName(chat.DisplayName).
		SetSessionUUID(chat.SessionUUID).
		SetStatus(chat.Status).
		Save(ctx)
	if err != nil {
		return err
	}
	applyIMChatEntity(chat, created)
	return nil
}

func (r *imBotRepository) RotateChatSessionUUID(ctx context.Context, chatID int64) (string, error) {
	newUUID, err := newIMSessionUUID()
	if err != nil {
		return "", err
	}
	err = r.client.IMBotChat.UpdateOneID(chatID).
		SetSessionUUID(newUUID).
		Exec(ctx)
	if err != nil {
		return "", err
	}
	return newUUID, nil
}

func (r *imBotRepository) ListChats(ctx context.Context, botID int64, params pagination.PaginationParams) ([]service.IMBotChat, *pagination.PaginationResult, error) {
	q := r.client.IMBotChat.Query().
		Where(imbotchat.BotID(botID))
	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, err
	}
	chats, err := q.
		Order(dbent.Desc(imbotchat.FieldLastMessageAt)).
		Offset(params.Offset()).
		Limit(params.Limit()).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := make([]service.IMBotChat, 0, len(chats))
	for i := range chats {
		out = append(out, *imChatEntityToService(chats[i]))
	}
	return out, paginationResultFromTotal(int64(total), params), nil
}

func (r *imBotRepository) UpdateChat(ctx context.Context, chatID int64, fields service.IMChatUpdateFields) error {
	builder := r.client.IMBotChat.UpdateOneID(chatID)
	if fields.Status != nil {
		builder = builder.SetStatus(*fields.Status)
	}
	if fields.ModelOverride != nil {
		builder = builder.SetModelOverride(*fields.ModelOverride)
	}
	if fields.DisplayName != nil {
		builder = builder.SetDisplayName(*fields.DisplayName)
	}
	return builder.Exec(ctx)
}

func (r *imBotRepository) DeleteChat(ctx context.Context, chatID int64) error {
	// messages cascade via FK
	err := r.client.IMBotChat.DeleteOneID(chatID).Exec(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return service.ErrIMChatNotFound
		}
		return err
	}
	return nil
}

// ---------- messages ----------

func (r *imBotRepository) AppendMessage(ctx context.Context, m *service.IMBotMessage) error {
	created, err := r.client.IMBotMessage.Create().
		SetChatID(m.ChatID).
		SetBotID(m.BotID).
		SetRole(m.Role).
		SetContent(m.Content).
		SetRequestID(m.RequestID).
		Save(ctx)
	if err != nil {
		return err
	}
	m.ID = created.ID
	m.CreatedAt = created.CreatedAt
	return nil
}

// ListRecentMessages returns the newest `limit` messages in chronological
// order (oldest first).
func (r *imBotRepository) ListRecentMessages(ctx context.Context, chatID int64, limit int) ([]service.IMBotMessage, error) {
	msgs, err := r.client.IMBotMessage.Query().
		Where(imbotmessage.ChatID(chatID)).
		Order(dbent.Desc(imbotmessage.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	// reverse to chronological
	out := make([]service.IMBotMessage, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		out = append(out, *imMsgEntityToService(msgs[i]))
	}
	return out, nil
}

func (r *imBotRepository) DeleteChatMessages(ctx context.Context, chatID int64) error {
	_, err := r.client.IMBotMessage.Delete().
		Where(imbotmessage.ChatID(chatID)).
		Exec(ctx)
	return err
}

func (r *imBotRepository) ListMessagesPage(ctx context.Context, chatID int64, params pagination.PaginationParams) ([]service.IMBotMessage, *pagination.PaginationResult, error) {
	q := r.client.IMBotMessage.Query().
		Where(imbotmessage.ChatID(chatID))
	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, err
	}
	msgs, err := q.
		Order(dbent.Desc(imbotmessage.FieldID)).
		Offset(params.Offset()).
		Limit(params.Limit()).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := make([]service.IMBotMessage, 0, len(msgs))
	for i := range msgs {
		out = append(out, *imMsgEntityToService(msgs[i]))
	}
	return out, paginationResultFromTotal(int64(total), params), nil
}

func (r *imBotRepository) SweepExpiredMessages(ctx context.Context, before time.Time) (int64, error) {
	n, err := r.client.IMBotMessage.Delete().
		Where(imbotmessage.CreatedAtLT(before)).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return int64(n), nil
}

// ---------- mapping ----------

func applyIMBotEntity(dst *service.IMBot, m *dbent.IMBot) {
	dst.ID = m.ID
	dst.Name = m.Name
	dst.Platform = m.Platform
	dst.CredentialsEnc = m.CredentialsEncrypted
	dst.APIKeyID = m.APIKeyID
	dst.ModelOverride = m.ModelOverride
	dst.SystemPrompt = m.SystemPrompt
	dst.Status = m.Status
	dst.MaxConcurrency = m.MaxConcurrency
	dst.HistoryMaxMessages = m.HistoryMaxMessages
	dst.PairingEnabled = m.PairingEnabled
	dst.LastError = m.LastError
	dst.CreatedAt = m.CreatedAt
	dst.UpdatedAt = m.UpdatedAt
}

func imBotEntityToService(m *dbent.IMBot) *service.IMBot {
	out := &service.IMBot{}
	applyIMBotEntity(out, m)
	return out
}

func applyIMChatEntity(dst *service.IMBotChat, m *dbent.IMBotChat) {
	dst.ID = m.ID
	dst.BotID = m.BotID
	dst.ChatID = m.ChatID
	dst.PlatformUserID = m.PlatformUserID
	dst.DisplayName = m.DisplayName
	dst.SessionUUID = m.SessionUUID
	dst.ModelOverride = m.ModelOverride
	dst.Status = m.Status
	dst.PairedAt = m.PairedAt
	dst.LastMessageAt = m.LastMessageAt
	dst.CreatedAt = m.CreatedAt
	dst.UpdatedAt = m.UpdatedAt
}

func imChatEntityToService(m *dbent.IMBotChat) *service.IMBotChat {
	out := &service.IMBotChat{}
	applyIMChatEntity(out, m)
	return out
}

func imMsgEntityToService(m *dbent.IMBotMessage) *service.IMBotMessage {
	return &service.IMBotMessage{
		ID:        m.ID,
		ChatID:    m.ChatID,
		BotID:     m.BotID,
		Role:      m.Role,
		Content:   m.Content,
		RequestID: m.RequestID,
		CreatedAt: m.CreatedAt,
	}
}

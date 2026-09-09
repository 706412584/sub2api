package im

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// pairing implements the pairing-code gate (cc-haha pairing.ts semantics):
// 6-char code from a confusion-safe alphabet, single-use, TTL'd, per-user
// failure rate limit; unknown chats are rejected by default.
type pairing struct {
	rdb redis.Cmdable
	ttl time.Duration
}

const (
	pairingCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // no 0/O/1/I/L
	pairingCodeLen      = 6

	pairingCodeKeyPrefix  = "im:pair:code:"
	pairingFailKeyPrefix  = "im:pair:fail:"
	pairingDenyHintPrefix = "im:pair:hint:"

	pairingMaxFails        = 5
	pairingFailWindow      = 5 * time.Minute
	pairingDenyHintSilence = 6 * time.Hour
)

// newPairing builds the pairing gate backed by Redis.
func newPairing(rdb redis.Cmdable, ttl time.Duration) *pairing {
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &pairing{rdb: rdb, ttl: ttl}
}

// Generate issues a fresh single-use code for a bot. The previous code (if
// any) is invalidated immediately.
func (p *pairing) Generate(ctx context.Context, botID int64) (string, time.Time, error) {
	code, err := randomPairingCode()
	if err != nil {
		return "", time.Time{}, err
	}
	key := fmt.Sprintf("%s%d", pairingCodeKeyPrefix, botID)
	ok, err := p.rdb.Set(ctx, key, code, p.ttl).Result()
	_ = ok
	if err != nil {
		return "", time.Time{}, err
	}
	return code, time.Now().Add(p.ttl), nil
}

var errPairingRateLimited = errors.New("pairing attempts rate limited")

// Verify consumes a code: on match it creates (or re-binds) the chat row
// with a fresh session UUID. Wrong/expired codes count against the user's
// failure budget; a valid code resets it.
func (p *pairing) Verify(ctx context.Context, repo service.IMBotRepository, bot *service.IMBot, code, platformUser, chatID, displayName string) (*service.IMBotChat, error) {
	failKey := pairingFailKeyPrefix + platformUser
	if fails, err := p.rdb.Get(ctx, failKey).Int(); err == nil && fails >= pairingMaxFails {
		return nil, errPairingRateLimited
	}

	codeKey := fmt.Sprintf("%s%d", pairingCodeKeyPrefix, bot.ID)
	stored, err := p.rdb.Get(ctx, codeKey).Result()
	if err != nil || strings.EqualFold(strings.TrimSpace(code), stored) == false || stored == "" {
		_ = p.rdb.Incr(ctx, failKey)
		_ = p.rdb.Expire(ctx, failKey, pairingFailWindow)
		return nil, service.ErrIMChatNotFound
	}
	// Consume the code (single use) and clear the failure budget.
	_ = p.rdb.Del(ctx, codeKey)
	_ = p.rdb.Del(ctx, failKey)

	sessionUUID, err := randomSessionUUID()
	if err != nil {
		return nil, err
	}
	chat := &service.IMBotChat{
		BotID:          bot.ID,
		ChatID:         chatID,
		PlatformUserID: platformUser,
		DisplayName:    displayName,
		SessionUUID:    sessionUUID,
		Status:         service.IMChatStatusActive,
	}
	if err := repo.UpsertChat(ctx, chat); err != nil {
		return nil, err
	}
	return chat, nil
}

// DenyHintAllowed rate-limits the "not paired" notice so an unpaired chat
// can't be spammed (at most one hint per window).
func (p *pairing) DenyHintAllowed(ctx context.Context, chatKey string) bool {
	key := pairingDenyHintPrefix + chatKey
	ok, err := p.rdb.SetNX(ctx, key, "1", pairingDenyHintSilence).Result()
	if err != nil {
		// Redis unavailable: prefer a silent drop over spam.
		logger.L().Warn("im.pairing deny-hint redis unavailable", zap.Error(err))
		return false
	}
	return ok
}

func randomPairingCode() (string, error) {
	var sb strings.Builder
	for i := 0; i < pairingCodeLen; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(pairingCodeAlphabet))))
		if err != nil {
			return "", err
		}
		sb.WriteByte(pairingCodeAlphabet[n.Int64()])
	}
	return sb.String(), nil
}

func randomSessionUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hexDigits[v>>4], hexDigits[v&0x0f])
	}
	return string(out), nil
}

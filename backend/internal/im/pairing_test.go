package im

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newTestPairing(t *testing.T) (*pairing, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return newPairing(rdb, time.Hour), mr
}

func TestPairing_GenerateAndVerify(t *testing.T) {
	p, _ := newTestPairing(t)
	ctx := context.Background()
	repo := &fakeRepo{}

	code, expiresAt, err := p.Generate(ctx, 7)
	require.NoError(t, err)
	require.Len(t, code, 6)
	require.True(t, time.Now().Before(expiresAt))
	for _, ch := range code {
		require.Contains(t, pairingCodeAlphabet, string(ch)) // no 0/O/1/I/L
	}

	bot := &service.IMBot{ID: 7, Platform: "telegram"}
	chat, err := p.Verify(ctx, repo, bot, code, "user-1", "chat-1", "Alice")
	require.NoError(t, err)
	require.Equal(t, "user-1", chat.PlatformUserID)
	require.Len(t, chat.SessionUUID, 36)
	require.Equal(t, service.IMChatStatusActive, chat.Status)
}

func TestPairing_SingleUse(t *testing.T) {
	p, _ := newTestPairing(t)
	ctx := context.Background()
	repo := &fakeRepo{}
	bot := &service.IMBot{ID: 7}

	code, _, err := p.Generate(ctx, 7)
	require.NoError(t, err)

	_, err = p.Verify(ctx, repo, bot, code, "u", "c1", "A")
	require.NoError(t, err)
	_, err = p.Verify(ctx, repo, bot, code, "u", "c2", "B")
	require.Error(t, err) // consumed
}

func TestPairing_FailureRateLimit(t *testing.T) {
	p, _ := newTestPairing(t)
	ctx := context.Background()
	repo := &fakeRepo{}
	bot := &service.IMBot{ID: 7}

	_, _, _ = p.Generate(ctx, 7)
	for i := 0; i < pairingMaxFails; i++ {
		_, err := p.Verify(ctx, repo, bot, "WRONG1", "u", "c", "A")
		require.Error(t, err)
	}
	// 6th attempt hits the rate limit even with the right code format
	_, _, _ = p.Generate(ctx, 7)
	code, _, _ := p.Generate(ctx, 7)
	_, err := p.Verify(ctx, repo, bot, code, "u", "c", "A")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "rate limited"))
}

func TestPairing_CaseInsensitiveTrim(t *testing.T) {
	p, _ := newTestPairing(t)
	ctx := context.Background()
	repo := &fakeRepo{}
	bot := &service.IMBot{ID: 7}

	code, _, err := p.Generate(ctx, 7)
	require.NoError(t, err)
	// lowercase + surrounding spaces still matches
	_, err = p.Verify(ctx, repo, bot, "  "+strings.ToLower(code)+"  ", "u", "c", "A")
	require.NoError(t, err)
}

func TestPairing_DenyHintOncePerWindow(t *testing.T) {
	p, _ := newTestPairing(t)
	ctx := context.Background()
	require.True(t, p.DenyHintAllowed(ctx, "k1"))
	require.False(t, p.DenyHintAllowed(ctx, "k1"))
	require.True(t, p.DenyHintAllowed(ctx, "k2"))
}

// fakeRepo implements the chat-upsert slice the pairing gate needs.
type fakeRepo struct {
	service.IMBotRepository // panics on unexpected calls
	chats                   map[string]*service.IMBotChat
}

func (f *fakeRepo) UpsertChat(_ context.Context, chat *service.IMBotChat) error {
	if f.chats == nil {
		f.chats = map[string]*service.IMBotChat{}
	}
	stored := *chat
	f.chats[chat.ChatID] = &stored
	return nil
}

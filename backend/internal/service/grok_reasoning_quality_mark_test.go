package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestResolveGrokReasoningQualitySoftPenalty(t *testing.T) {
	store := NewMemoryGrokReasoningQualityMarkStore()
	proxyID := int64(42)
	account := &Account{ID: 1, Platform: PlatformGrok, ProxyID: &proxyID}

	require.Equal(t, 0.0, ResolveGrokReasoningQualitySoftPenalty(context.Background(), store, account))

	require.NoError(t, store.Set(context.Background(), &GrokReasoningQualityMark{
		ProxyID:  proxyID,
		Status:   GrokReasoningProbeStatusVisible,
		ProbedAt: time.Now().Unix(),
	}, time.Hour))
	require.Equal(t, 0.0, ResolveGrokReasoningQualitySoftPenalty(context.Background(), store, account))

	require.NoError(t, store.Set(context.Background(), &GrokReasoningQualityMark{
		ProxyID:  proxyID,
		Status:   GrokReasoningProbeStatusEncryptedOnly,
		ProbedAt: time.Now().Unix(),
	}, time.Hour))
	require.Equal(t, grokReasoningQualitySoftPenalty, ResolveGrokReasoningQualitySoftPenalty(context.Background(), store, account))

	require.NoError(t, store.Set(context.Background(), &GrokReasoningQualityMark{
		ProxyID:  proxyID,
		Status:   GrokReasoningProbeStatusNoReasoning,
		ProbedAt: time.Now().Unix(),
	}, time.Hour))
	require.Equal(t, grokReasoningQualitySoftPenalty, ResolveGrokReasoningQualitySoftPenalty(context.Background(), store, account))

	openai := &Account{ID: 2, Platform: PlatformOpenAI, ProxyID: &proxyID}
	require.Equal(t, 0.0, ResolveGrokReasoningQualitySoftPenalty(context.Background(), store, openai))
}

func TestGrokReasoningProbeService_PersistsQualityMark(t *testing.T) {
	account := &Account{
		ID:       9,
		Name:     "credential-account",
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "access-secret",
			"refresh_token": "refresh-secret",
			"expires_at":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		},
	}
	selectedProxy := &Proxy{ID: 5, Protocol: "socks5", Host: "127.0.0.1", Port: 27890}
	store := NewMemoryGrokReasoningQualityMarkStore()
	svc := &GrokReasoningProbeService{
		accountRepo:       &grokReasoningProbeAccountRepoStub{account: account},
		proxyRepo:         &grokReasoningProbeProxyRepoStub{proxies: map[int64]*Proxy{5: selectedProxy}},
		grokTokenProvider: &grokReasoningProbeTokenStub{token: "access-secret"},
		httpUpstream:      &grokReasoningProbeUpstreamStub{},
		markStore:         store,
	}

	result, err := svc.Probe(context.Background(), 5, GrokReasoningProbeRequest{
		AccountID:        account.ID,
		ConfirmQuotaCost: true,
	})
	require.NoError(t, err)
	require.Equal(t, GrokReasoningProbeStatusVisible, result.Status)

	mark, err := store.Get(context.Background(), 5)
	require.NoError(t, err)
	require.NotNil(t, mark)
	require.Equal(t, GrokReasoningProbeStatusVisible, mark.Status)
	require.Equal(t, int64(5), mark.ProxyID)
}

//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestResolveGrokReasoningVisibilityConfig(t *testing.T) {
	ctx := context.Background()

	t.Run("no setting service returns off", func(t *testing.T) {
		svc := &OpenAIGatewayService{settingService: nil}
		cfg := svc.resolveGrokReasoningVisibilityConfig(ctx, nil)
		require.Equal(t, GrokReasoningVisibilityModeOff, cfg.Mode)
		require.Equal(t, 0, cfg.ProbeTTLSec)
	})

	t.Run("gateway mode and ttl propagate", func(t *testing.T) {
		settingSvc := NewSettingService(&settingRepoStub{
			values: map[string]string{
				SettingKeyGrokReasoningVisibility: `{"mode":"enforce","probe_ttl_sec":300}`,
			},
		}, &config.Config{})
		svc := &OpenAIGatewayService{settingService: settingSvc}
		cfg := svc.resolveGrokReasoningVisibilityConfig(ctx, nil)
		require.Equal(t, GrokReasoningVisibilityModeEnforce, cfg.Mode)
		require.Equal(t, 300, cfg.ProbeTTLSec)
	})

	t.Run("group inherit falls back to gateway ttl", func(t *testing.T) {
		settingSvc := NewSettingService(&settingRepoStub{
			values: map[string]string{
				SettingKeyGrokReasoningVisibility: `{"mode":"enforce","probe_ttl_sec":600}`,
			},
		}, &config.Config{})
		svc := &OpenAIGatewayService{settingService: settingSvc}
		groupID := int64(1)
		group := &Group{
			ID:                          groupID,
			Platform:                    PlatformGrok,
			GrokReasoningVisibilityMode: "inherit",
			GrokReasoningProbeTTLSec:    -1,
		}
		ctxWithGroup := context.WithValue(ctx, ctxkey.Group, group)
		cfg := svc.resolveGrokReasoningVisibilityConfig(ctxWithGroup, &groupID)
		require.Equal(t, GrokReasoningVisibilityModeEnforce, cfg.Mode)
		require.Equal(t, 600, cfg.ProbeTTLSec) // gateway ttl
	})

	t.Run("group overrides mode and ttl", func(t *testing.T) {
		settingSvc := NewSettingService(&settingRepoStub{
			values: map[string]string{
				SettingKeyGrokReasoningVisibility: `{"mode":"off","probe_ttl_sec":0}`,
			},
		}, &config.Config{})
		svc := &OpenAIGatewayService{settingService: settingSvc}
		groupID := int64(1)
		group := &Group{
			ID:                          groupID,
			Platform:                    PlatformGrok,
			GrokReasoningVisibilityMode: "enforce",
			GrokReasoningProbeTTLSec:    0,
		}
		ctxWithGroup := context.WithValue(ctx, ctxkey.Group, group)
		cfg := svc.resolveGrokReasoningVisibilityConfig(ctxWithGroup, &groupID)
		require.Equal(t, GrokReasoningVisibilityModeEnforce, cfg.Mode)
		require.Equal(t, 0, cfg.ProbeTTLSec)
	})

	t.Run("group ttl -1 inherits gateway", func(t *testing.T) {
		settingSvc := NewSettingService(&settingRepoStub{
			values: map[string]string{
				SettingKeyGrokReasoningVisibility: `{"mode":"enforce","probe_ttl_sec":120}`,
			},
		}, &config.Config{})
		svc := &OpenAIGatewayService{settingService: settingSvc}
		groupID := int64(1)
		group := &Group{
			ID:                          groupID,
			Platform:                    PlatformGrok,
			GrokReasoningVisibilityMode: "enforce",
			GrokReasoningProbeTTLSec:    -1,
		}
		ctxWithGroup := context.WithValue(ctx, ctxkey.Group, group)
		cfg := svc.resolveGrokReasoningVisibilityConfig(ctxWithGroup, &groupID)
		require.Equal(t, 120, cfg.ProbeTTLSec)
	})
}

func TestRejectGrokAccountByReasoning_MarkStore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryGrokReasoningQualityMarkStore()
	settingSvc := NewSettingService(&settingRepoStub{
		values: map[string]string{
			SettingKeyGrokReasoningVisibility: `{"mode":"enforce","probe_ttl_sec":3600}`,
		},
	}, &config.Config{})
	svc := &OpenAIGatewayService{
		settingService:            settingSvc,
		grokReasoningQualityMarks: store,
		// No probe service — tests the mark-only path when ProbeTTLSec > 0
	}

	groupID := int64(1)
	group := &Group{
		ID:                          groupID,
		Platform:                    PlatformGrok,
		GrokReasoningVisibilityMode: "enforce",
		GrokReasoningProbeTTLSec:    3600,
	}
	ctxWithGroup := context.WithValue(ctx, ctxkey.Group, group)

	grokAccount := &Account{ID: 100, Platform: PlatformGrok, Type: AccountTypeOAuth}

	t.Run("no mark calls probe service but fails open", func(t *testing.T) {
		rejected, reason := svc.rejectGrokAccountByReasoning(ctxWithGroup, grokAccount, &groupID)
		require.False(t, rejected)
		require.Equal(t, "", reason)
	})

	t.Run("visible mark is not rejected", func(t *testing.T) {
		require.NoError(t, store.Set(ctx, &GrokReasoningQualityMark{
			AccountID: grokAccount.ID,
			Status:    GrokReasoningProbeStatusVisible,
		}, time.Hour))
		rejected, reason := svc.rejectGrokAccountByReasoning(ctxWithGroup, grokAccount, &groupID)
		require.False(t, rejected)
		require.Equal(t, "", reason)
	})

	t.Run("encrypted_only mark is rejected", func(t *testing.T) {
		require.NoError(t, store.Set(ctx, &GrokReasoningQualityMark{
			AccountID: grokAccount.ID,
			Status:    GrokReasoningProbeStatusEncryptedOnly,
		}, time.Hour))
		rejected, reason := svc.rejectGrokAccountByReasoning(ctxWithGroup, grokAccount, &groupID)
		require.True(t, rejected)
		require.Equal(t, "encrypted_only", reason)
	})

	t.Run("no_reasoning mark is rejected", func(t *testing.T) {
		require.NoError(t, store.Set(ctx, &GrokReasoningQualityMark{
			AccountID: grokAccount.ID,
			Status:    GrokReasoningProbeStatusNoReasoning,
		}, time.Hour))
		rejected, reason := svc.rejectGrokAccountByReasoning(ctxWithGroup, grokAccount, &groupID)
		require.True(t, rejected)
		require.Equal(t, "no_reasoning", reason)
	})

	t.Run("non-grok account is never rejected", func(t *testing.T) {
		openAIAccount := &Account{ID: 200, Platform: PlatformOpenAI}
		rejected, _ := svc.rejectGrokAccountByReasoning(ctxWithGroup, openAIAccount, &groupID)
		require.False(t, rejected)
	})

	t.Run("nil account is not rejected", func(t *testing.T) {
		rejected, _ := svc.rejectGrokAccountByReasoning(ctxWithGroup, nil, &groupID)
		require.False(t, rejected)
	})

	t.Run("off mode does not reject", func(t *testing.T) {
		// Use a group whose mode is off in the ctx so the group setting wins.
		offGroupID := int64(1)
		offGroup := &Group{
			ID:                          offGroupID,
			Platform:                    PlatformGrok,
			GrokReasoningVisibilityMode: "off",
		}
		offCtx := context.WithValue(ctx, ctxkey.Group, offGroup)
		offSvc := &OpenAIGatewayService{
			settingService: NewSettingService(&settingRepoStub{
				values: map[string]string{
					SettingKeyGrokReasoningVisibility: `{"mode":"enforce"}`,
				},
			}, &config.Config{}),
			grokReasoningQualityMarks: store,
		}
		rejected, _ := offSvc.rejectGrokAccountByReasoning(offCtx, grokAccount, &offGroupID)
		require.False(t, rejected)
	})
}

func TestRejectGrokAccountByReasoning_ProbeTTLZero(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryGrokReasoningQualityMarkStore()
	settingSvc := NewSettingService(&settingRepoStub{
		values: map[string]string{
			SettingKeyGrokReasoningVisibility: `{"mode":"enforce","probe_ttl_sec":0}`,
		},
	}, &config.Config{})
	svc := &OpenAIGatewayService{
		settingService:            settingSvc,
		grokReasoningQualityMarks: store,
		// No probe service → fail-open when ttl=0 and no mark
	}

	groupID := int64(1)
	group := &Group{
		ID:                          groupID,
		Platform:                    PlatformGrok,
		GrokReasoningVisibilityMode: "enforce",
		GrokReasoningProbeTTLSec:    0,
	}
	ctxWithGroup := context.WithValue(ctx, ctxkey.Group, group)
	grokAccount := &Account{ID: 100, Platform: PlatformGrok, Type: AccountTypeOAuth}

	t.Run("ttl=0 skips mark cache, no probe svc fails open", func(t *testing.T) {
		// Even with a visible mark, ttl=0 means "always re-probe" — but since
		// there's no probe service, it fails open.
		require.NoError(t, store.Set(ctx, &GrokReasoningQualityMark{
			AccountID: grokAccount.ID,
			Status:    GrokReasoningProbeStatusVisible,
		}, time.Hour))
		rejected, _ := svc.rejectGrokAccountByReasoning(ctxWithGroup, grokAccount, &groupID)
		require.False(t, rejected)
	})
}

func TestResolveAccountProbeProxyID(t *testing.T) {
	svc := &OpenAIGatewayService{}

	t.Run("account proxy_id wins", func(t *testing.T) {
		proxyID := int64(42)
		account := &Account{ProxyID: &proxyID}
		require.Equal(t, int64(42), svc.resolveAccountProbeProxyID(account))
	})

	t.Run("account.Proxy.ID fallback", func(t *testing.T) {
		account := &Account{Proxy: &Proxy{ID: 99}}
		require.Equal(t, int64(99), svc.resolveAccountProbeProxyID(account))
	})

	t.Run("nil account returns 0", func(t *testing.T) {
		require.Equal(t, int64(0), svc.resolveAccountProbeProxyID(nil))
	})
}
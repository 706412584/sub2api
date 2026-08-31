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
	t.Run("legacy gateway json defaults quarantine to 120", func(t *testing.T) {
		settingSvc := NewSettingService(&settingRepoStub{
			values: map[string]string{
				SettingKeyGrokReasoningVisibility: `{"mode":"enforce","probe_ttl_sec":60}`,
			},
		}, &config.Config{})
		svc := &OpenAIGatewayService{settingService: settingSvc}
		cfg := svc.resolveGrokReasoningVisibilityConfig(ctx, nil)
		require.Equal(t, GrokReasoningVisibilityQuarantineDefaultSec, cfg.QuarantineSec)
	})

	t.Run("gateway quarantine propagates", func(t *testing.T) {
		settingSvc := NewSettingService(&settingRepoStub{
			values: map[string]string{
				SettingKeyGrokReasoningVisibility: `{"mode":"enforce","probe_ttl_sec":0,"quarantine_sec":30}`,
			},
		}, &config.Config{})
		svc := &OpenAIGatewayService{settingService: settingSvc}
		cfg := svc.resolveGrokReasoningVisibilityConfig(ctx, nil)
		require.Equal(t, 30, cfg.QuarantineSec)
	})

	t.Run("group quarantine -1 inherits gateway", func(t *testing.T) {
		settingSvc := NewSettingService(&settingRepoStub{
			values: map[string]string{
				SettingKeyGrokReasoningVisibility: `{"mode":"enforce","quarantine_sec":90}`,
			},
		}, &config.Config{})
		svc := &OpenAIGatewayService{settingService: settingSvc}
		groupID := int64(1)
		group := &Group{
			ID:                          groupID,
			Platform:                    PlatformGrok,
			GrokReasoningVisibilityMode: "enforce",
			GrokReasoningQuarantineSec:  -1,
		}
		cfg := svc.resolveGrokReasoningVisibilityConfig(context.WithValue(ctx, ctxkey.Group, group), &groupID)
		require.Equal(t, 90, cfg.QuarantineSec)
	})

	t.Run("group quarantine 0 and -2 override", func(t *testing.T) {
		settingSvc := NewSettingService(&settingRepoStub{
			values: map[string]string{
				SettingKeyGrokReasoningVisibility: `{"mode":"enforce","quarantine_sec":120}`,
			},
		}, &config.Config{})
		svc := &OpenAIGatewayService{settingService: settingSvc}
		groupID := int64(1)

		zeroGroup := &Group{ID: groupID, Platform: PlatformGrok, GrokReasoningQuarantineSec: 0}
		cfg := svc.resolveGrokReasoningVisibilityConfig(context.WithValue(ctx, ctxkey.Group, zeroGroup), &groupID)
		require.Equal(t, 0, cfg.QuarantineSec)

		pauseGroup := &Group{ID: groupID, Platform: PlatformGrok, GrokReasoningQuarantineSec: GrokReasoningQuarantinePauseSchedulable}
		cfg = svc.resolveGrokReasoningVisibilityConfig(context.WithValue(ctx, ctxkey.Group, pauseGroup), &groupID)
		require.Equal(t, GrokReasoningQuarantinePauseSchedulable, cfg.QuarantineSec)
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

// quarantineAccountRepoStub records pause / temp-unsched writes for apply tests.
type quarantineAccountRepoStub struct {
	AccountRepository
	pausedIDs []int64
	tempCalls []struct {
		id     int64
		until  time.Time
		reason string
	}
}

func (r *quarantineAccountRepoStub) SetSchedulable(_ context.Context, id int64, schedulable bool) error {
	if !schedulable {
		r.pausedIDs = append(r.pausedIDs, id)
	}
	return nil
}

func (r *quarantineAccountRepoStub) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.tempCalls = append(r.tempCalls, struct {
		id     int64
		until  time.Time
		reason string
	}{id: id, until: until, reason: reason})
	return nil
}

func TestApplyGrokReasoningVisibilityQuarantine(t *testing.T) {
	ctx := context.Background()
	decision := GrokReasoningVisibilityDecision{Status: GrokReasoningProbeStatusEncryptedOnly}
	groupID := int64(7)

	t.Run("N seconds writes temp unschedulable", func(t *testing.T) {
		repo := &quarantineAccountRepoStub{}
		settingSvc := NewSettingService(&settingRepoStub{
			values: map[string]string{
				SettingKeyGrokReasoningVisibility: `{"mode":"enforce","quarantine_sec":45}`,
			},
		}, &config.Config{})
		svc := &OpenAIGatewayService{settingService: settingSvc, accountRepo: repo}
		before := time.Now()
		svc.applyGrokReasoningVisibilityQuarantine(ctx, 42, decision, GrokReasoningVisibilityModeEnforce, nil)
		require.Len(t, repo.tempCalls, 1)
		require.Empty(t, repo.pausedIDs)
		require.Equal(t, int64(42), repo.tempCalls[0].id)
		require.True(t, repo.tempCalls[0].until.After(before.Add(40*time.Second)))
		require.True(t, repo.tempCalls[0].until.Before(before.Add(50*time.Second)))
	})

	t.Run("0 skips temp unschedulable", func(t *testing.T) {
		repo := &quarantineAccountRepoStub{}
		settingSvc := NewSettingService(&settingRepoStub{
			values: map[string]string{
				SettingKeyGrokReasoningVisibility: `{"mode":"enforce","quarantine_sec":120}`,
			},
		}, &config.Config{})
		svc := &OpenAIGatewayService{settingService: settingSvc, accountRepo: repo}
		gctx := context.WithValue(ctx, ctxkey.Group, &Group{
			ID: groupID, Platform: PlatformGrok,
			GrokReasoningVisibilityMode: "enforce",
			GrokReasoningQuarantineSec:  0,
		})
		svc.applyGrokReasoningVisibilityQuarantine(gctx, 42, decision, GrokReasoningVisibilityModeEnforce, &groupID)
		require.Empty(t, repo.tempCalls)
		require.Empty(t, repo.pausedIDs)
	})

	t.Run("-2 pauses scheduling", func(t *testing.T) {
		repo := &quarantineAccountRepoStub{}
		settingSvc := NewSettingService(&settingRepoStub{
			values: map[string]string{
				SettingKeyGrokReasoningVisibility: `{"mode":"enforce","quarantine_sec":120}`,
			},
		}, &config.Config{})
		svc := &OpenAIGatewayService{settingService: settingSvc, accountRepo: repo}
		gctx := context.WithValue(ctx, ctxkey.Group, &Group{
			ID: groupID, Platform: PlatformGrok,
			GrokReasoningVisibilityMode: "enforce",
			GrokReasoningQuarantineSec:  GrokReasoningQuarantinePauseSchedulable,
		})
		svc.applyGrokReasoningVisibilityQuarantine(gctx, 99, decision, GrokReasoningVisibilityModeEnforce, &groupID)
		require.Empty(t, repo.tempCalls)
		require.Equal(t, []int64{99}, repo.pausedIDs)
	})

	t.Run("soft mode without quarantine until is no-op", func(t *testing.T) {
		repo := &quarantineAccountRepoStub{}
		settingSvc := NewSettingService(&settingRepoStub{
			values: map[string]string{
				SettingKeyGrokReasoningVisibility: `{"mode":"soft","quarantine_sec":120}`,
			},
		}, &config.Config{})
		svc := &OpenAIGatewayService{settingService: settingSvc, accountRepo: repo}
		svc.applyGrokReasoningVisibilityQuarantine(ctx, 42, decision, GrokReasoningVisibilityModeSoft, nil)
		require.Empty(t, repo.tempCalls)
		require.Empty(t, repo.pausedIDs)
	})
}

// TestApplyGrokReasoningVisibilityQuarantineSplit 验证按拒绝原因智能分流：
//   - encrypted_only / no_reasoning（探测结论确定）→ 按配置执行（-2 永久暂停 / N 秒）
//   - error / no_proxy / probe_failed（可见性未知：网络/代理/上游错误）
//     → 降级为短冷却，-2 与 0 都不生效，永不永久暂停
func TestApplyGrokReasoningVisibilityQuarantineSplit(t *testing.T) {
	ctx := context.Background()
	groupID := int64(7)

	newEnforceSvc := func(quarantineSec int) (*OpenAIGatewayService, *quarantineAccountRepoStub, context.Context) {
		repo := &quarantineAccountRepoStub{}
		settingSvc := NewSettingService(&settingRepoStub{
			values: map[string]string{
				SettingKeyGrokReasoningVisibility: `{"mode":"enforce","quarantine_sec":120}`,
			},
		}, &config.Config{})
		svc := &OpenAIGatewayService{settingService: settingSvc, accountRepo: repo}
		gctx := context.WithValue(ctx, ctxkey.Group, &Group{
			ID:                          groupID,
			Platform:                    PlatformGrok,
			GrokReasoningVisibilityMode: "enforce",
			GrokReasoningQuarantineSec:  quarantineSec,
		})
		return svc, repo, gctx
	}

	t.Run("error status with -2 degrades to default cooldown, never pauses", func(t *testing.T) {
		svc, repo, gctx := newEnforceSvc(GrokReasoningQuarantinePauseSchedulable)
		decision := GrokReasoningVisibilityDecision{Excluded: true, Status: GrokReasoningProbeStatusError}
		before := time.Now()
		svc.applyGrokReasoningVisibilityQuarantine(gctx, 42, decision, GrokReasoningVisibilityModeEnforce, &groupID)
		require.Empty(t, repo.pausedIDs, "transport error must never permanently pause")
		require.Len(t, repo.tempCalls, 1)
		require.Equal(t, int64(42), repo.tempCalls[0].id)
		require.True(t, repo.tempCalls[0].until.After(before.Add(110*time.Second)))
		require.True(t, repo.tempCalls[0].until.Before(before.Add(130*time.Second)), "degraded to 120s default cooldown")
	})

	t.Run("error status with 0 degrades to default cooldown", func(t *testing.T) {
		svc, repo, gctx := newEnforceSvc(0)
		decision := GrokReasoningVisibilityDecision{Excluded: true, Status: GrokReasoningProbeStatusError}
		svc.applyGrokReasoningVisibilityQuarantine(gctx, 42, decision, GrokReasoningVisibilityModeEnforce, &groupID)
		require.Empty(t, repo.pausedIDs)
		require.Len(t, repo.tempCalls, 1, "0 must not skip cooldown for unknown-visibility errors")
	})

	t.Run("no_proxy status with -2 degrades to cooldown", func(t *testing.T) {
		svc, repo, gctx := newEnforceSvc(GrokReasoningQuarantinePauseSchedulable)
		decision := GrokReasoningVisibilityDecision{Excluded: true, Status: GrokReasoningProbeStatus("no_proxy")}
		svc.applyGrokReasoningVisibilityQuarantine(gctx, 42, decision, GrokReasoningVisibilityModeEnforce, &groupID)
		require.Empty(t, repo.pausedIDs)
		require.Len(t, repo.tempCalls, 1)
	})

	t.Run("probe_failed status with -2 degrades to cooldown", func(t *testing.T) {
		svc, repo, gctx := newEnforceSvc(GrokReasoningQuarantinePauseSchedulable)
		decision := GrokReasoningVisibilityDecision{Excluded: true, Status: GrokReasoningProbeStatus("probe_failed")}
		svc.applyGrokReasoningVisibilityQuarantine(gctx, 42, decision, GrokReasoningVisibilityModeEnforce, &groupID)
		require.Empty(t, repo.pausedIDs)
		require.Len(t, repo.tempCalls, 1)
	})

	t.Run("error status with configured N keeps N seconds", func(t *testing.T) {
		svc, repo, gctx := newEnforceSvc(300)
		decision := GrokReasoningVisibilityDecision{Excluded: true, Status: GrokReasoningProbeStatusError}
		before := time.Now()
		svc.applyGrokReasoningVisibilityQuarantine(gctx, 42, decision, GrokReasoningVisibilityModeEnforce, &groupID)
		require.Empty(t, repo.pausedIDs)
		require.Len(t, repo.tempCalls, 1)
		require.True(t, repo.tempCalls[0].until.After(before.Add(290*time.Second)))
		require.True(t, repo.tempCalls[0].until.Before(before.Add(310*time.Second)), "configured N-second cooldown kept for errors")
	})

	t.Run("encrypted_only verdict with -2 still pauses", func(t *testing.T) {
		svc, repo, gctx := newEnforceSvc(GrokReasoningQuarantinePauseSchedulable)
		decision := GrokReasoningVisibilityDecision{Excluded: true, Status: GrokReasoningProbeStatusEncryptedOnly}
		svc.applyGrokReasoningVisibilityQuarantine(gctx, 42, decision, GrokReasoningVisibilityModeEnforce, &groupID)
		require.Equal(t, []int64{42}, repo.pausedIDs, "durable verdict keeps -2 permanent pause")
		require.Empty(t, repo.tempCalls)
	})

	t.Run("no_reasoning verdict with -2 still pauses", func(t *testing.T) {
		svc, repo, gctx := newEnforceSvc(GrokReasoningQuarantinePauseSchedulable)
		decision := GrokReasoningVisibilityDecision{Excluded: true, Status: GrokReasoningProbeStatusNoReasoning}
		svc.applyGrokReasoningVisibilityQuarantine(gctx, 42, decision, GrokReasoningVisibilityModeEnforce, &groupID)
		require.Equal(t, []int64{42}, repo.pausedIDs)
		require.Empty(t, repo.tempCalls)
	})

	t.Run("encrypted_only verdict with 0 still excludes round only", func(t *testing.T) {
		svc, repo, gctx := newEnforceSvc(0)
		decision := GrokReasoningVisibilityDecision{Excluded: true, Status: GrokReasoningProbeStatusEncryptedOnly}
		svc.applyGrokReasoningVisibilityQuarantine(gctx, 42, decision, GrokReasoningVisibilityModeEnforce, &groupID)
		require.Empty(t, repo.pausedIDs)
		require.Empty(t, repo.tempCalls, "durable verdict keeps 0 = exclude-this-round semantics")
	})
}

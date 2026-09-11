//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAntigravityTierRank tier 序号基础语义
func TestAntigravityTierRank(t *testing.T) {
	newAcc := func(platform string, planType string) *Account {
		acc := &Account{ID: 1, Platform: platform}
		if planType != "" {
			acc.Credentials = map[string]any{"plan_type": planType}
		}
		return acc
	}

	require.Equal(t, 0, antigravityTierRank(newAcc(PlatformAntigravity, "Ultra")))
	require.Equal(t, 0, antigravityTierRank(newAcc(PlatformAntigravity, "ULTRA")))
	require.Equal(t, 1, antigravityTierRank(newAcc(PlatformAntigravity, "Pro")))
	require.Equal(t, 1, antigravityTierRank(newAcc(PlatformAntigravity, "PRO")))
	require.Equal(t, 2, antigravityTierRank(newAcc(PlatformAntigravity, "Free")))
	require.Equal(t, 3, antigravityTierRank(newAcc(PlatformAntigravity, "")))
	require.Equal(t, 3, antigravityTierRank(newAcc(PlatformAntigravity, "unknown")))
	require.Equal(t, 3, antigravityTierRank(newAcc(PlatformAnthropic, "Ultra")), "非 Antigravity 平台恒为 3")
}

// TestCompareAntigravityTier 跨平台配对不干预
func TestCompareAntigravityTier(t *testing.T) {
	antigravity := &Account{ID: 1, Platform: PlatformAntigravity, Credentials: map[string]any{"plan_type": "Ultra"}}
	anthropic := &Account{ID: 2, Platform: PlatformAnthropic, Credentials: map[string]any{"plan_type": "Ultra"}}

	_, _, applies := compareAntigravityTier(antigravity, anthropic)
	require.False(t, applies, "跨平台配对不干预")
	_, _, applies = compareAntigravityTier(anthropic, antigravity)
	require.False(t, applies, "跨平台配对不干预（反向）")

	ultra := &Account{ID: 3, Platform: PlatformAntigravity, Credentials: map[string]any{"plan_type": "Ultra"}}
	pro := &Account{ID: 4, Platform: PlatformAntigravity, Credentials: map[string]any{"plan_type": "Pro"}}

	accTier, selTier, applies := compareAntigravityTier(ultra, pro)
	require.True(t, applies)
	require.Equal(t, 0, accTier)
	require.Equal(t, 1, selTier)

	_, _, applies = compareAntigravityTier(pro, pro)
	require.True(t, applies, "同 tier applies=true，由调用方回退 LRU")
}

// TestSelectAccountForModelWithPlatform_AntigravityTierPriority 同优先级
// Antigravity 账号池内：ULTRA 优先于 PRO/FREE，与 LastUsedAt 顺序无关；
// tier 平级时回退 LRU；跨平台账号排序不受影响。
func TestSelectAccountForModelWithPlatform_AntigravityTierPriority(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	buildRepo := func(accounts ...Account) *mockAccountRepoForPlatform {
		repo := &mockAccountRepoForPlatform{
			accounts:     accounts,
			accountsByID: map[int64]*Account{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
		}
		return repo
	}

	newSvc := func(repo *mockAccountRepoForPlatform) *GatewayService {
		return &GatewayService{
			accountRepo: repo,
			cache:       &mockGatewayCacheForPlatform{},
			cfg:         testConfig(),
		}
	}

	t.Run("ULTRA 优先于更久未使用的 PRO", func(t *testing.T) {
		repo := buildRepo(
			Account{ID: 1, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"plan_type": "Ultra"}, LastUsedAt: ptr(now)},
			Account{ID: 2, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"plan_type": "Pro"}, LastUsedAt: ptr(now.Add(-2 * time.Hour))},
		)
		acc, err := newSvc(repo).selectAccountForModelWithPlatform(ctx, nil, "", "claude-sonnet-4-5", nil, PlatformAntigravity)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(1), acc.ID, "ULTRA 应优先，即使 PRO 更久未使用")
	})

	t.Run("FREE 后进也应被 ULTRA 替代", func(t *testing.T) {
		repo := buildRepo(
			Account{ID: 1, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"plan_type": "Free"}},
			Account{ID: 2, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"plan_type": "Ultra"}},
		)
		acc, err := newSvc(repo).selectAccountForModelWithPlatform(ctx, nil, "", "claude-sonnet-4-5", nil, PlatformAntigravity)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.ID, "FREE 先选中也应被后进池的 ULTRA 替代")
	})

	t.Run("同 tier 回退 LRU", func(t *testing.T) {
		repo := buildRepo(
			Account{ID: 1, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"plan_type": "Pro"}, LastUsedAt: ptr(now)},
			Account{ID: 2, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"plan_type": "Pro"}, LastUsedAt: ptr(now.Add(-2 * time.Hour))},
		)
		acc, err := newSvc(repo).selectAccountForModelWithPlatform(ctx, nil, "", "claude-sonnet-4-5", nil, PlatformAntigravity)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.ID, "同 tier 应回退 LRU（更久未使用者胜出）")
	})

	t.Run("无 plan_type 的老账号正常参与 LRU", func(t *testing.T) {
		repo := buildRepo(
			Account{ID: 1, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true,
				LastUsedAt: ptr(now.Add(-1 * time.Hour))},
			Account{ID: 2, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"plan_type": "Free"}, LastUsedAt: ptr(now.Add(-2 * time.Hour))},
		)
		acc, err := newSvc(repo).selectAccountForModelWithPlatform(ctx, nil, "", "claude-sonnet-4-5", nil, PlatformAntigravity)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.ID, "无 tier（rank 3）与 FREE 同级，走 LRU")
	})

	t.Run("Priority 仍是第一优先级", func(t *testing.T) {
		repo := buildRepo(
			Account{ID: 1, Platform: PlatformAntigravity, Priority: 2, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"plan_type": "Ultra"}},
			Account{ID: 2, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"plan_type": "Free"}},
		)
		acc, err := newSvc(repo).selectAccountForModelWithPlatform(ctx, nil, "", "claude-sonnet-4-5", nil, PlatformAntigravity)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.ID, "低 Priority 数字的 FREE 仍胜过高 Priority 的 ULTRA")
	})

	t.Run("混合平台排序不受 tier 干预", func(t *testing.T) {
		// anthropic 账号（无 tier）与 antigravity ULTRA 同优先级：
		// 不干预 → 走原 LRU 逻辑，更久未使用的 anthropic 胜出
		repo := buildRepo(
			Account{ID: 1, Platform: PlatformAnthropic, Priority: 1, Status: StatusActive, Schedulable: true,
				LastUsedAt: ptr(now.Add(-3 * time.Hour))},
			Account{ID: 2, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"plan_type": "Ultra"}, LastUsedAt: ptr(now)},
		)
		acc, err := newSvc(repo).selectAccountForModelWithPlatform(ctx, nil, "", "claude-sonnet-4-5", nil, PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(1), acc.ID, "anthropic 平台选择不应被 antigravity tier 影响")
	})
}

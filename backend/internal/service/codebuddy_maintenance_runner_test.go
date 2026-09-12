//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/stretchr/testify/require"
)

// maintenanceAccountRepo 养号任务测试用账号仓库。
type maintenanceAccountRepo struct {
	AccountRepository

	mu              sync.Mutex
	accounts        []Account
	clearRateLimit  []int64
	clearTempUnsch  []int64
	setError        []int64
	updated         []int64
	updatedAccounts []*Account
}

func (r *maintenanceAccountRepo) ListByPlatform(_ context.Context, platform string) ([]Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Account, 0, len(r.accounts))
	for _, a := range r.accounts {
		if a.Platform == platform {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *maintenanceAccountRepo) ClearRateLimit(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearRateLimit = append(r.clearRateLimit, id)
	return nil
}

func (r *maintenanceAccountRepo) ClearTempUnschedulable(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearTempUnsch = append(r.clearTempUnsch, id)
	return nil
}

func (r *maintenanceAccountRepo) SetError(_ context.Context, id int64, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setError = append(r.setError, id)
	return nil
}

func (r *maintenanceAccountRepo) Update(_ context.Context, account *Account) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updated = append(r.updated, account.ID)
	copied := *account
	r.updatedAccounts = append(r.updatedAccounts, &copied)
	return nil
}

// maintenanceSettingRepo 养号设置测试仓库。
type maintenanceSettingRepo struct {
	mu       sync.Mutex
	settings *CodebuddyMaintenanceSettings
}

func (r *maintenanceSettingRepo) GetValue(context.Context, string) (string, error) {
	return "", errSettingNotFoundTest
}

func (r *maintenanceSettingRepo) Set(context.Context, string, string) error {
	return nil
}

func (r *maintenanceSettingRepo) Delete(context.Context, string) error {
	return nil
}

func (r *maintenanceSettingRepo) Get(context.Context, string) (*Setting, error) {
	return nil, errSettingNotFoundTest
}

func (r *maintenanceSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, nil
}

func (r *maintenanceSettingRepo) SetMultiple(context.Context, map[string]string) error {
	return nil
}

func (r *maintenanceSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return nil, nil
}

var errSettingNotFoundTest = fmt.Errorf("setting not found")

func newMaintenanceTestService(repo *maintenanceAccountRepo, upstream HTTPUpstream, settings *CodebuddyMaintenanceSettings) (*CodebuddyMaintenanceRunner, *SettingService) {
	if settings == nil {
		settings = DefaultCodebuddyMaintenanceSettings()
	}
	stored := &maintenanceSettingRepo{settings: settings}
	settingService := &SettingService{settingRepo: stored}

	gateway := NewCodebuddyGatewayService(upstream, nil, nil, nil)
	refresher := NewCodebuddyTokenRefresher(upstream, nil)
	runner := NewCodebuddyMaintenanceRunner(repo, gateway, refresher, settingService, nil)
	return runner, settingService
}

func maintenanceCNAccount(id int64) Account {
	return Account{
		ID:       id,
		Platform: PlatformCodebuddy,
		Type:     codebuddy.AccountTypeCN,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token":  "cb-access",
			"refresh_token": "cb-refresh",
			"uid":           "u-1",
		},
	}
}

func TestCodebuddyMaintenanceListCNAccountsFilters(t *testing.T) {
	repo := &maintenanceAccountRepo{accounts: []Account{
		maintenanceCNAccount(1),
		{ID: 2, Platform: PlatformCodebuddy, Type: codebuddy.AccountTypeGlobal, Status: StatusActive}, // Global 跳过
		{ID: 3, Platform: PlatformCodebuddy, Type: codebuddy.AccountTypeCN, Status: StatusError},      // 非 active 跳过
		{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive},               // 非 codebuddy 平台
	}}
	runner, _ := newMaintenanceTestService(repo, nil, nil)
	accounts := runner.listCNAccounts(context.Background())
	require.Len(t, accounts, 1)
	require.Equal(t, int64(1), accounts[0].ID)
}

func TestCodebuddyMaintenanceCheckinUnfreezesOnPositiveCredit(t *testing.T) {
	// 上游序列：签到 200 → 余额查询 200（CycleCapacityRemain=100）
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		codebuddyRefreshResponse(`{"code":0,"msg":"ok","data":{}}`, http.StatusOK),
		codebuddyRefreshResponse(`{"code":0,"msg":"ok","data":{"Response":{"Data":{"Accounts":[{"CycleCapacitySize":100,"CycleCapacityRemain":100,"CapacityRemain":100}]}}}}`, http.StatusOK),
	}}
	future := time.Now().Add(time.Hour)
	account := maintenanceCNAccount(1)
	account.RateLimitResetAt = &future
	repo := &maintenanceAccountRepo{accounts: []Account{account}}
	runner, _ := newMaintenanceTestService(repo, upstream, nil)

	runner.runCheckin(context.Background(), DefaultCodebuddyMaintenanceSettings())

	require.Equal(t, []int64{1}, repo.clearRateLimit, "余额>0 必须清除限流冷却")
	require.Empty(t, repo.clearTempUnsch, "无临时不可调度状态不需清除")
}

func TestCodebuddyMaintenanceCheckinKeepsCooldownOnZeroCredit(t *testing.T) {
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		codebuddyRefreshResponse(`{"code":0,"msg":"ok","data":{}}`, http.StatusOK),
		codebuddyRefreshResponse(`{"code":0,"msg":"ok","data":{"Response":{"Data":{"Accounts":[{"CycleCapacitySize":0,"CycleCapacityRemain":0,"CapacityRemain":0}]}}}}`, http.StatusOK),
	}}
	future := time.Now().Add(time.Hour)
	account := maintenanceCNAccount(1)
	account.RateLimitResetAt = &future
	repo := &maintenanceAccountRepo{accounts: []Account{account}}
	runner, _ := newMaintenanceTestService(repo, upstream, nil)

	runner.runCheckin(context.Background(), DefaultCodebuddyMaintenanceSettings())

	require.Empty(t, repo.clearRateLimit, "余额为 0 不得解冻")
}

func TestCodebuddyMaintenanceCheckinBusinessErrorStillQueriesCredit(t *testing.T) {
	// 已签到（业务 code != 0）仍要查余额（可能今天已签但昨天冻结待解）
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		codebuddyRefreshResponse(`{"code":10001,"msg":"已签到"}`, http.StatusOK),
		codebuddyRefreshResponse(`{"code":0,"msg":"ok","data":{"Response":{"Data":{"Accounts":[{"CycleCapacitySize":100,"CycleCapacityRemain":50,"CapacityRemain":50}]}}}}`, http.StatusOK),
	}}
	future := time.Now().Add(time.Hour)
	account := maintenanceCNAccount(1)
	account.TempUnschedulableUntil = &future
	repo := &maintenanceAccountRepo{accounts: []Account{account}}
	runner, _ := newMaintenanceTestService(repo, upstream, nil)

	runner.runCheckin(context.Background(), DefaultCodebuddyMaintenanceSettings())

	require.Equal(t, []int64{1}, repo.clearTempUnsch, "已签到但仍需按余额解冻临时不可调度")
}

func TestCodebuddyMaintenanceActivityReportsWithUserID(t *testing.T) {
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		codebuddyRefreshResponse(`{"code":0,"msg":"ok","data":{}}`, http.StatusOK),
	}}
	repo := &maintenanceAccountRepo{accounts: []Account{maintenanceCNAccount(1)}}
	runner, _ := newMaintenanceTestService(repo, upstream, nil)

	runner.runActivity(context.Background(), DefaultCodebuddyMaintenanceSettings())

	require.Len(t, upstream.requests, 1)
	req := upstream.requests[0]
	require.Contains(t, req.URL.Path, "/v2/report")
	body := readRequestBody(t, req)
	// 事件必须是数组形，且必须含 userId（缺失则服务端静默丢弃）
	require.True(t, strings.HasPrefix(string(body), "["), "上报 body 必须是数组")
	require.Contains(t, string(body), `"userId":"u-1"`)
	require.Contains(t, string(body), `"eventCode":"chat_request_send"`)
}

func TestCodebuddyMaintenanceKeepaliveSessionDeadDisables(t *testing.T) {
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		codebuddyRefreshResponse(`{"code":12153,"msg":"Offline user session not found"}`, http.StatusOK),
	}}
	repo := &maintenanceAccountRepo{accounts: []Account{maintenanceCNAccount(1)}}
	runner, _ := newMaintenanceTestService(repo, upstream, nil)

	runner.runKeepalive(context.Background(), DefaultCodebuddyMaintenanceSettings())

	require.Equal(t, []int64{1}, repo.setError, "12153 session 死亡必须 SetError 禁用")
	require.Empty(t, repo.updated, "禁用路径不回写凭证")
}

func TestCodebuddyMaintenanceKeepaliveSuccessSavesCredentials(t *testing.T) {
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		codebuddyRefreshResponse(`{"code":0,"msg":"ok","data":{"accessToken":"new-token","expiresIn":3600}}`, http.StatusOK),
	}}
	repo := &maintenanceAccountRepo{accounts: []Account{maintenanceCNAccount(1)}}
	runner, _ := newMaintenanceTestService(repo, upstream, nil)

	runner.runKeepalive(context.Background(), DefaultCodebuddyMaintenanceSettings())

	require.Empty(t, repo.setError)
	require.Len(t, repo.updatedAccounts, 1, "刷新成功必须回写凭证")
	require.Equal(t, "new-token", repo.updatedAccounts[0].Credentials["access_token"])
}

func TestCodebuddyMaintenanceTickHonorsSettings(t *testing.T) {
	// 关闭全部任务时 tick 不产生任何上游请求
	upstream := &queuedHTTPUpstream{}
	repo := &maintenanceAccountRepo{accounts: []Account{maintenanceCNAccount(1)}}
	settings := &CodebuddyMaintenanceSettings{Enabled: false}
	runner, _ := newMaintenanceTestService(repo, upstream, settings)

	runner.tick()

	require.Empty(t, upstream.requests)
}

func TestCodebuddyMaintenanceHourMatching(t *testing.T) {
	require.True(t, codebuddyHourMatches([]int{9, 21}, 9))
	require.True(t, codebuddyHourMatches([]int{9, 21}, 21))
	require.False(t, codebuddyHourMatches([]int{9, 21}, 10))
}

func TestNormalizeCodebuddyMaintenanceSettings(t *testing.T) {
	// 空小时表回落默认；非法小时钳 0-23；去重排序
	normalized := normalizeCodebuddyMaintenanceSettings(&CodebuddyMaintenanceSettings{
		CheckinHours:   nil,
		ActivityHours:  []int{25, -3, 10, 10},
		KeepaliveHours: []int{22, 22},
		AccountDelayMs: -1,
	})
	require.Equal(t, []int{9, 21}, normalized.CheckinHours)
	require.Equal(t, []int{0, 10, 23}, normalized.ActivityHours)
	require.Equal(t, []int{22}, normalized.KeepaliveHours)
	require.Equal(t, 800, normalized.AccountDelayMs)
}

func TestCodebuddyMaintenanceRunExclusiveDedup(t *testing.T) {
	// 同类任务不并发：第一次未返回时，第二次同步调用必须直接跳过
	repo := &maintenanceAccountRepo{}
	runner, _ := newMaintenanceTestService(repo, nil, nil)

	runner.mu.Lock()
	runner.inFlight["checkin"] = true
	runner.mu.Unlock()

	ran := false
	runner.runExclusive("checkin", nil, func(context.Context, *CodebuddyMaintenanceSettings) {
		ran = true
	})
	require.False(t, ran, "in-flight 任务未完成时不应重入")

	runner.mu.Lock()
	delete(runner.inFlight, "checkin")
	runner.mu.Unlock()
	runner.runExclusive("checkin", nil, func(context.Context, *CodebuddyMaintenanceSettings) {})
}

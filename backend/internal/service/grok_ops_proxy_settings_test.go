package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type grokOpsProxySettingRepo struct {
	mu     sync.Mutex
	values map[string]string
}

func (r *grokOpsProxySettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *grokOpsProxySettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *grokOpsProxySettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

func (r *grokOpsProxySettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *grokOpsProxySettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]string)
	}
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func (r *grokOpsProxySettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func (r *grokOpsProxySettingRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, key)
	return nil
}

type grokOpsProxyRepoStub struct {
	proxies map[int64]*Proxy
}

func (r *grokOpsProxyRepoStub) Create(context.Context, *Proxy) error { return nil }
func (r *grokOpsProxyRepoStub) GetByID(_ context.Context, id int64) (*Proxy, error) {
	if r == nil || r.proxies == nil {
		return nil, ErrProxyNotFound
	}
	proxy := r.proxies[id]
	if proxy == nil {
		return nil, ErrProxyNotFound
	}
	return proxy, nil
}
func (r *grokOpsProxyRepoStub) ListByIDs(context.Context, []int64) ([]Proxy, error) {
	return nil, nil
}
func (r *grokOpsProxyRepoStub) Update(context.Context, *Proxy) error { return nil }
func (r *grokOpsProxyRepoStub) Delete(context.Context, int64) error  { return nil }
func (r *grokOpsProxyRepoStub) List(context.Context, pagination.PaginationParams) ([]Proxy, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *grokOpsProxyRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string) ([]Proxy, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *grokOpsProxyRepoStub) ListWithFiltersAndAccountCount(context.Context, pagination.PaginationParams, string, string, string) ([]ProxyWithAccountCount, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *grokOpsProxyRepoStub) ListActive(context.Context) ([]Proxy, error) { return nil, nil }
func (r *grokOpsProxyRepoStub) ListActiveWithAccountCount(context.Context) ([]ProxyWithAccountCount, error) {
	return nil, nil
}
func (r *grokOpsProxyRepoStub) ListOwnedByPrefix(context.Context, string) ([]ProxyWithAccountCount, error) {
	return nil, nil
}
func (r *grokOpsProxyRepoStub) ExistsByHostPortAuth(context.Context, string, int, string, string) (bool, error) {
	return false, nil
}
func (r *grokOpsProxyRepoStub) CountAccountsByProxyID(context.Context, int64) (int64, error) {
	return 0, nil
}
func (r *grokOpsProxyRepoStub) ListAccountSummariesByProxyID(context.Context, int64) ([]ProxyAccountSummary, error) {
	return nil, nil
}
func (r *grokOpsProxyRepoStub) SweepExpiredProxies(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (r *grokOpsProxyRepoStub) ListAllForFallback(context.Context) ([]Proxy, error) { return nil, nil }
func (r *grokOpsProxyRepoStub) CountExpired(context.Context) (int64, error)         { return 0, nil }
func (r *grokOpsProxyRepoStub) CountExpiringSoon(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func newGrokOpsProxyTestService(repo SettingRepository, proxyRepo ProxyRepository) *SettingService {
	svc := NewSettingService(repo, &config.Config{})
	svc.proxyRepo = proxyRepo
	return svc
}

func TestGetGrokOpsProxySettings_DefaultsWhenMissing(t *testing.T) {
	svc := newGrokOpsProxyTestService(&grokOpsProxySettingRepo{}, nil)
	settings, err := svc.GetGrokOpsProxySettings(context.Background())
	require.NoError(t, err)
	require.NotNil(t, settings)
	require.False(t, settings.Enabled)
	require.Nil(t, settings.ProxyID)
	require.False(t, settings.ApplyToRefresh)
}

func TestSetAndGetGrokOpsProxySettings_RoundTrip(t *testing.T) {
	repo := &grokOpsProxySettingRepo{values: map[string]string{}}
	proxyID := int64(7)
	proxyRepo := &grokOpsProxyRepoStub{proxies: map[int64]*Proxy{
		7: {ID: 7, Name: "ops", Protocol: "http", Host: "127.0.0.1", Port: 8080, Status: StatusActive},
	}}
	svc := newGrokOpsProxyTestService(repo, proxyRepo)

	require.NoError(t, svc.SetGrokOpsProxySettings(context.Background(), &GrokOpsProxySettings{
		Enabled:        true,
		ProxyID:        &proxyID,
		ApplyToRefresh: true,
	}))

	settings, err := svc.GetGrokOpsProxySettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	require.NotNil(t, settings.ProxyID)
	require.Equal(t, int64(7), *settings.ProxyID)
	require.True(t, settings.ApplyToRefresh)

	raw, ok := repo.values[SettingKeyGrokOpsProxy]
	require.True(t, ok)
	var stored GrokOpsProxySettings
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	require.True(t, stored.Enabled)
}

func TestResolveGrokOpsProxyOverride_DisabledAndKeepBound(t *testing.T) {
	svc := newGrokOpsProxyTestService(&grokOpsProxySettingRepo{}, nil)
	override, err := svc.ResolveGrokOpsProxyOverride(context.Background())
	require.NoError(t, err)
	require.Nil(t, override)

	repo := &grokOpsProxySettingRepo{values: map[string]string{}}
	svc = newGrokOpsProxyTestService(repo, nil)
	require.NoError(t, svc.SetGrokOpsProxySettings(context.Background(), &GrokOpsProxySettings{Enabled: true, ProxyID: nil}))
	override, err = svc.ResolveGrokOpsProxyOverride(context.Background())
	require.NoError(t, err)
	require.Nil(t, override)
}

func TestResolveGrokOpsProxyOverride_ForceDirectAndProxy(t *testing.T) {
	directID := int64(0)
	proxyID := int64(9)
	proxyRepo := &grokOpsProxyRepoStub{proxies: map[int64]*Proxy{
		9: {ID: 9, Name: "ops9", Protocol: "socks5", Host: "10.0.0.9", Port: 1080, Status: StatusActive},
	}}
	repo := &grokOpsProxySettingRepo{values: map[string]string{}}
	svc := newGrokOpsProxyTestService(repo, proxyRepo)

	require.NoError(t, svc.SetGrokOpsProxySettings(context.Background(), &GrokOpsProxySettings{Enabled: true, ProxyID: &directID}))
	override, err := svc.ResolveGrokOpsProxyOverride(context.Background())
	require.NoError(t, err)
	require.NotNil(t, override)
	require.True(t, override.ForceDirect)

	require.NoError(t, svc.SetGrokOpsProxySettings(context.Background(), &GrokOpsProxySettings{Enabled: true, ProxyID: &proxyID}))
	override, err = svc.ResolveGrokOpsProxyOverride(context.Background())
	require.NoError(t, err)
	require.NotNil(t, override)
	require.False(t, override.ForceDirect)
	require.NotNil(t, override.Proxy)
	require.Equal(t, "socks5://10.0.0.9:1080", override.Proxy.URL())
}

func TestResolveGrokOpsProxyURL_RefreshGate(t *testing.T) {
	proxyID := int64(3)
	proxyRepo := &grokOpsProxyRepoStub{proxies: map[int64]*Proxy{
		3: {ID: 3, Protocol: "http", Host: "ops.example", Port: 3128, Status: StatusActive},
	}}
	repo := &grokOpsProxySettingRepo{values: map[string]string{}}
	svc := newGrokOpsProxyTestService(repo, proxyRepo)
	require.NoError(t, svc.SetGrokOpsProxySettings(context.Background(), &GrokOpsProxySettings{
		Enabled:        true,
		ProxyID:        &proxyID,
		ApplyToRefresh: false,
	}))

	url, forceDirect, ok, err := svc.ResolveGrokOpsProxyURL(context.Background(), false)
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, forceDirect)
	require.Equal(t, "http://ops.example:3128", url)

	url, forceDirect, ok, err = svc.ResolveGrokOpsProxyURL(context.Background(), true)
	require.NoError(t, err)
	require.False(t, ok)
	require.False(t, forceDirect)
	require.Empty(t, url)

	require.NoError(t, svc.SetGrokOpsProxySettings(context.Background(), &GrokOpsProxySettings{
		Enabled:        true,
		ProxyID:        &proxyID,
		ApplyToRefresh: true,
	}))
	url, forceDirect, ok, err = svc.ResolveGrokOpsProxyURL(context.Background(), true)
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, forceDirect)
	require.Equal(t, "http://ops.example:3128", url)
}

func TestApplyGrokOpsProxyOverrideIfNeeded_SkipsExplicitOverride(t *testing.T) {
	proxyID := int64(5)
	proxyRepo := &grokOpsProxyRepoStub{proxies: map[int64]*Proxy{
		5: {ID: 5, Protocol: "http", Host: "ops", Port: 1, Status: StatusActive},
	}}
	repo := &grokOpsProxySettingRepo{values: map[string]string{}}
	settingSvc := newGrokOpsProxyTestService(repo, proxyRepo)
	require.NoError(t, settingSvc.SetGrokOpsProxySettings(context.Background(), &GrokOpsProxySettings{Enabled: true, ProxyID: &proxyID}))

	explicit := &Proxy{ID: 99, Protocol: "http", Host: "explicit", Port: 9, Status: StatusActive}
	ctx := withTestProxyOverride(context.Background(), &TestProxyOverride{Proxy: explicit})
	svc := &AccountTestService{settingService: settingSvc}
	account := &Account{ID: 1, Platform: PlatformGrok}
	out := svc.applyGrokOpsProxyOverrideIfNeeded(ctx, account)
	got, ok := out.Value(testProxyOverrideContextKey{}).(*TestProxyOverride)
	require.True(t, ok)
	require.NotNil(t, got)
	require.NotNil(t, got.Proxy)
	require.Equal(t, int64(99), got.Proxy.ID)

	out = svc.applyGrokOpsProxyOverrideIfNeeded(context.Background(), account)
	got, ok = out.Value(testProxyOverrideContextKey{}).(*TestProxyOverride)
	require.True(t, ok)
	require.NotNil(t, got.Proxy)
	require.Equal(t, int64(5), got.Proxy.ID)
}

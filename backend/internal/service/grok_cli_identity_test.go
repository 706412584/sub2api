package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type grokCLIIdentitySettingRepo struct {
	mu     sync.Mutex
	values map[string]string
}

func (r *grokCLIIdentitySettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *grokCLIIdentitySettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *grokCLIIdentitySettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

func (r *grokCLIIdentitySettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *grokCLIIdentitySettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]string)
	}
	for k, v := range settings {
		r.values[k] = v
	}
	return nil
}

func (r *grokCLIIdentitySettingRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, key)
	return nil
}

func (r *grokCLIIdentitySettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.values))
	for k, v := range r.values {
		out[k] = v
	}
	return out, nil
}

func newGrokCLIIdentitySettingService(t *testing.T) *SettingService {
	t.Helper()
	repo := &grokCLIIdentitySettingRepo{values: map[string]string{}}
	return &SettingService{settingRepo: repo}
}

func TestIsSupportedGrokCLIVersion(t *testing.T) {
	require.True(t, IsSupportedGrokCLIVersion(GrokCLIPinnedStableVersion))
	require.True(t, IsSupportedGrokCLIVersion("0.2.121"))
	require.True(t, IsSupportedGrokCLIVersion("0.2.121-alpha.1"))
	require.False(t, IsSupportedGrokCLIVersion(""))
	require.False(t, IsSupportedGrokCLIVersion("0.2.119"))
	require.False(t, IsSupportedGrokCLIVersion(GrokCLIPinnedStableVersion+"-beta.1"))
	require.False(t, IsSupportedGrokCLIVersion("0.2.121+build.1"))
}

func TestResolveGrokCLIClientVersion_Precedence(t *testing.T) {
	t.Setenv(GrokCLIVersionEnvKey, "")
	PublishGrokCLIIdentitySettingsVersion("")
	t.Cleanup(func() { PublishGrokCLIIdentitySettingsVersion("") })

	v, source := ResolveGrokCLIClientVersionSource()
	require.Equal(t, GrokCLIPinnedStableVersion, v)
	require.Equal(t, "default", source)

	t.Setenv(GrokCLIVersionEnvKey, "0.2.121")
	v, source = ResolveGrokCLIClientVersionSource()
	require.Equal(t, "0.2.121", v)
	require.Equal(t, "env", source)

	PublishGrokCLIIdentitySettingsVersion("0.2.122")
	v, source = ResolveGrokCLIClientVersionSource()
	require.Equal(t, "0.2.122", v)
	require.Equal(t, "settings", source)
}

func TestSetAndGetGrokCLIIdentitySettings_RoundTrip(t *testing.T) {
	t.Setenv(GrokCLIVersionEnvKey, "")
	PublishGrokCLIIdentitySettingsVersion("")
	t.Cleanup(func() { PublishGrokCLIIdentitySettingsVersion("") })

	svc := newGrokCLIIdentitySettingService(t)
	require.NoError(t, svc.SetGrokCLIIdentitySettings(context.Background(), &GrokCLIIdentitySettings{
		Version: "0.2.122",
	}))
	require.Equal(t, "0.2.122", CurrentGrokCLIIdentitySettingsVersion())

	got, err := svc.GetGrokCLIIdentitySettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, "0.2.122", got.Version)

	status, err := svc.GetGrokCLIIdentityStatus(context.Background())
	require.NoError(t, err)
	require.Equal(t, "0.2.122", status.EffectiveVersion)
	require.Equal(t, "settings", status.Source)
	require.Equal(t, GrokCLIPinnedStableVersion, status.PinnedDefault)

	status, err = svc.RestoreGrokCLIIdentityDefault(context.Background())
	require.NoError(t, err)
	require.Equal(t, "", CurrentGrokCLIIdentitySettingsVersion())
	require.Equal(t, GrokCLIPinnedStableVersion, status.EffectiveVersion)
	require.Equal(t, "default", status.Source)
}

func TestSetGrokCLIIdentitySettings_RejectsUnsupported(t *testing.T) {
	svc := newGrokCLIIdentitySettingService(t)
	err := svc.SetGrokCLIIdentitySettings(context.Background(), &GrokCLIIdentitySettings{Version: "0.2.119"})
	require.Error(t, err)
}

func TestCheckGrokCLIIdentityLatest_UsesNPM(t *testing.T) {
	t.Setenv(GrokCLIVersionEnvKey, "")
	PublishGrokCLIIdentitySettingsVersion("")
	t.Cleanup(func() {
		PublishGrokCLIIdentitySettingsVersion("")
		grokCLILatestCache.Store((*cachedGrokCLILatest)(nil))
		grokCLINPMRegistryURL = "https://registry.npmjs.org/@xai-official/grok"
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"dist-tags": map[string]string{"latest": "0.2.200"},
		})
	}))
	t.Cleanup(server.Close)
	grokCLINPMRegistryURL = server.URL

	svc := newGrokCLIIdentitySettingService(t)
	status, err := svc.CheckGrokCLIIdentityLatest(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, "0.2.200", status.LatestVersion)
	require.True(t, status.UpdateAvailable)

	status, err = svc.ApplyGrokCLIIdentityLatest(context.Background())
	require.NoError(t, err)
	require.Equal(t, "0.2.200", status.EffectiveVersion)
	require.Equal(t, "settings", status.Source)
	require.Equal(t, "0.2.200", CurrentGrokCLIIdentitySettingsVersion())
}

func TestGetGrokCLIIdentityStatus_UpdateAvailableFromCache(t *testing.T) {
	t.Setenv(GrokCLIVersionEnvKey, "")
	PublishGrokCLIIdentitySettingsVersion("")
	t.Cleanup(func() {
		PublishGrokCLIIdentitySettingsVersion("")
		grokCLILatestCache.Store((*cachedGrokCLILatest)(nil))
	})

	now := time.Now()
	grokCLILatestCache.Store(&cachedGrokCLILatest{
		version:   "0.2.200",
		checkedAt: now,
		expiresAt: now.Add(grokCLILatestCacheTTL),
	})

	svc := newGrokCLIIdentitySettingService(t)
	status, err := svc.GetGrokCLIIdentityStatus(context.Background())
	require.NoError(t, err)
	require.Equal(t, "0.2.200", status.LatestVersion)
	require.True(t, status.UpdateAvailable)
	require.Equal(t, GrokCLIPinnedStableVersion, status.EffectiveVersion)
}

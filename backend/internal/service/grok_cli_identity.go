package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"golang.org/x/mod/semver"
)

func init() {
	// Wire billing headers to the same effective CLI version as live/transport paths.
	// pkg/xai cannot import service; service publishes the resolver instead.
	xai.SetCLIClientVersionResolver(ResolveGrokCLIClientVersion)
}

const (
	// GrokCLIPinnedStableVersion is the compile-time minimum/default identity
	// reported to cli-chat-proxy. Keep in sync with repository.grokCLIStableVersion
	// consumers via ResolveGrokCLIClientVersion.
	GrokCLIPinnedStableVersion = "0.2.118"

	// GrokCLIVersionEnvKey allows process-level override without settings DB.
	GrokCLIVersionEnvKey = "XAI_GROK_CLI_VERSION"

	grokCLINPMPackage        = "@xai-official/grok"
	grokCLILatestCacheTTL    = 10 * time.Minute
	grokCLILatestHTTPTimeout = 12 * time.Second
	grokCLILatestBodyLimit   = 1 << 20
)

// Overridable in tests.
var grokCLINPMRegistryURL = "https://registry.npmjs.org/@xai-official/grok"

// Runtime settings override published for the hot path (http_upstream).
// Empty string means "no settings override".
var grokCLIIdentitySettingsOverride atomic.Value // string

type cachedGrokCLILatest struct {
	version   string
	checkedAt time.Time
	expiresAt time.Time
}

var grokCLILatestCache atomic.Value // *cachedGrokCLILatest

// PublishGrokCLIIdentitySettingsVersion updates the process-local override used by
// ResolveGrokCLIClientVersion. Invalid versions are ignored (treated as empty).
func PublishGrokCLIIdentitySettingsVersion(version string) {
	v := strings.TrimSpace(version)
	if v != "" && !IsSupportedGrokCLIVersion(v) {
		v = ""
	}
	grokCLIIdentitySettingsOverride.Store(v)
}

// CurrentGrokCLIIdentitySettingsVersion returns the published settings override (may be empty).
func CurrentGrokCLIIdentitySettingsVersion() string {
	if v, ok := grokCLIIdentitySettingsOverride.Load().(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// IsSupportedGrokCLIVersion reports whether version is a canonical semver at or
// above the pinned stable minimum (prereleases below the next minor are rejected
// when they sort before the minimum).
func IsSupportedGrokCLIVersion(version string) bool {
	version = strings.TrimSpace(version)
	if version == "" {
		return false
	}
	canonical := "v" + version
	minimum := "v" + GrokCLIPinnedStableVersion
	return semver.IsValid(canonical) &&
		semver.Canonical(canonical) == canonical &&
		semver.Compare(canonical, minimum) >= 0
}

// ResolveGrokCLIClientVersion returns settings override > env > pinned default.
func ResolveGrokCLIClientVersion() string {
	if v := CurrentGrokCLIIdentitySettingsVersion(); IsSupportedGrokCLIVersion(v) {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(GrokCLIVersionEnvKey)); IsSupportedGrokCLIVersion(v) {
		return v
	}
	return GrokCLIPinnedStableVersion
}

// ResolveGrokCLIClientVersionSource returns the effective version and its source.
func ResolveGrokCLIClientVersionSource() (version, source string) {
	if v := CurrentGrokCLIIdentitySettingsVersion(); IsSupportedGrokCLIVersion(v) {
		return v, "settings"
	}
	if v := strings.TrimSpace(os.Getenv(GrokCLIVersionEnvKey)); IsSupportedGrokCLIVersion(v) {
		return v, "env"
	}
	return GrokCLIPinnedStableVersion, "default"
}

func normalizeGrokCLIIdentitySettings(settings *GrokCLIIdentitySettings) *GrokCLIIdentitySettings {
	out := DefaultGrokCLIIdentitySettings()
	if settings == nil {
		return out
	}
	out.Version = strings.TrimSpace(settings.Version)
	return out
}

// GetGrokCLIIdentitySettings loads the persisted override (empty version = none).
func (s *SettingService) GetGrokCLIIdentitySettings(ctx context.Context) (*GrokCLIIdentitySettings, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultGrokCLIIdentitySettings(), nil
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyGrokCLIIdentity)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultGrokCLIIdentitySettings(), nil
		}
		return nil, fmt.Errorf("get grok cli identity settings: %w", err)
	}
	if strings.TrimSpace(value) == "" {
		return DefaultGrokCLIIdentitySettings(), nil
	}
	var settings GrokCLIIdentitySettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return DefaultGrokCLIIdentitySettings(), nil
	}
	return normalizeGrokCLIIdentitySettings(&settings), nil
}

// SetGrokCLIIdentitySettings persists the override and publishes it for hot path.
// Empty version clears the override. Non-empty must pass IsSupportedGrokCLIVersion.
func (s *SettingService) SetGrokCLIIdentitySettings(ctx context.Context, settings *GrokCLIIdentitySettings) error {
	if s == nil || s.settingRepo == nil {
		return fmt.Errorf("setting service unavailable")
	}
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	normalized := normalizeGrokCLIIdentitySettings(settings)
	if normalized.Version != "" && !IsSupportedGrokCLIVersion(normalized.Version) {
		return fmt.Errorf("unsupported grok cli version %q (need canonical semver >= %s)", normalized.Version, GrokCLIPinnedStableVersion)
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal grok cli identity settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyGrokCLIIdentity, string(data)); err != nil {
		return err
	}
	PublishGrokCLIIdentitySettingsVersion(normalized.Version)
	return nil
}

// WarmGrokCLIIdentitySettings loads DB override into the process-local hot path.
func (s *SettingService) WarmGrokCLIIdentitySettings(ctx context.Context) {
	settings, err := s.GetGrokCLIIdentitySettings(ctx)
	if err != nil || settings == nil {
		PublishGrokCLIIdentitySettingsVersion("")
		return
	}
	PublishGrokCLIIdentitySettingsVersion(settings.Version)
}

// GetGrokCLIIdentityStatus builds the admin status view (uses cached latest if any).
func (s *SettingService) GetGrokCLIIdentityStatus(ctx context.Context) (*GrokCLIIdentityStatus, error) {
	settings, err := s.GetGrokCLIIdentitySettings(ctx)
	if err != nil {
		return nil, err
	}
	// Ensure runtime override matches DB (multi-instance: each process warms itself).
	if settings != nil {
		PublishGrokCLIIdentitySettingsVersion(settings.Version)
	}

	effective, source := ResolveGrokCLIClientVersionSource()
	envRaw := strings.TrimSpace(os.Getenv(GrokCLIVersionEnvKey))
	envOverride := ""
	if IsSupportedGrokCLIVersion(envRaw) {
		envOverride = envRaw
	}

	status := &GrokCLIIdentityStatus{
		EffectiveVersion: effective,
		PinnedDefault:    GrokCLIPinnedStableVersion,
		SettingsOverride: "",
		EnvOverride:      envOverride,
		Source:           source,
	}
	if settings != nil {
		status.SettingsOverride = settings.Version
	}
	if cached, ok := grokCLILatestCache.Load().(*cachedGrokCLILatest); ok && cached != nil && cached.version != "" {
		status.LatestVersion = cached.version
		status.LatestCheckedAt = cached.checkedAt.UTC().Format(time.RFC3339)
		status.UpdateAvailable = semver.Compare("v"+cached.version, "v"+effective) > 0
	}
	return status, nil
}

// CheckGrokCLIIdentityLatest fetches npm dist-tags.latest for @xai-official/grok.
func (s *SettingService) CheckGrokCLIIdentityLatest(ctx context.Context, force bool) (*GrokCLIIdentityStatus, error) {
	if !force {
		if cached, ok := grokCLILatestCache.Load().(*cachedGrokCLILatest); ok && cached != nil {
			if time.Now().Before(cached.expiresAt) && cached.version != "" {
				return s.GetGrokCLIIdentityStatus(ctx)
			}
		}
	}

	latest, err := fetchGrokCLINPMLatest(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	grokCLILatestCache.Store(&cachedGrokCLILatest{
		version:   latest,
		checkedAt: now,
		expiresAt: now.Add(grokCLILatestCacheTTL),
	})
	return s.GetGrokCLIIdentityStatus(ctx)
}

// ApplyGrokCLIIdentityLatest checks npm latest and pins it as settings override.
func (s *SettingService) ApplyGrokCLIIdentityLatest(ctx context.Context) (*GrokCLIIdentityStatus, error) {
	status, err := s.CheckGrokCLIIdentityLatest(ctx, true)
	if err != nil {
		return nil, err
	}
	if status == nil || strings.TrimSpace(status.LatestVersion) == "" {
		return nil, fmt.Errorf("npm latest version unavailable")
	}
	if err := s.SetGrokCLIIdentitySettings(ctx, &GrokCLIIdentitySettings{Version: status.LatestVersion}); err != nil {
		return nil, err
	}
	return s.GetGrokCLIIdentityStatus(ctx)
}

// RestoreGrokCLIIdentityDefault clears the settings override.
func (s *SettingService) RestoreGrokCLIIdentityDefault(ctx context.Context) (*GrokCLIIdentityStatus, error) {
	if err := s.SetGrokCLIIdentitySettings(ctx, DefaultGrokCLIIdentitySettings()); err != nil {
		return nil, err
	}
	return s.GetGrokCLIIdentityStatus(ctx)
}

func fetchGrokCLINPMLatest(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(ctx, grokCLILatestHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, grokCLINPMRegistryURL, nil)
	if err != nil {
		return "", fmt.Errorf("build npm request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "sub2api-grok-cli-identity-check/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch npm package %s: %w", grokCLINPMPackage, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("npm registry returned %d for %s", resp.StatusCode, grokCLINPMPackage)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, grokCLILatestBodyLimit))
	if err != nil {
		return "", fmt.Errorf("read npm response: %w", err)
	}
	var payload struct {
		DistTags map[string]string `json:"dist-tags"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("parse npm response: %w", err)
	}
	latest := strings.TrimSpace(payload.DistTags["latest"])
	if latest == "" {
		return "", fmt.Errorf("npm dist-tags.latest missing for %s", grokCLINPMPackage)
	}
	// Accept any canonical semver from npm as "latest"; apply path still validates >= pin.
	canonical := "v" + latest
	if !semver.IsValid(canonical) || semver.Canonical(canonical) != canonical {
		return "", fmt.Errorf("npm latest is not canonical semver: %q", latest)
	}
	return latest, nil
}

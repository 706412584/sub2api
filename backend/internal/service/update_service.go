package service

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrNoUpdateAvailable         = infraerrors.Conflict("ALREADY_UP_TO_DATE", "no update available; current version is latest")
	ErrRollbackVersionNotAllowed = infraerrors.BadRequest("ROLLBACK_VERSION_NOT_ALLOWED", "version is not in the allowed rollback list")
)

const (
	updateCacheKey = "update_check_cache"
	updateCacheTTL = 1200 // 20 minutes
	// defaultUpdateGitHubRepo is used when UPDATE_GITHUB_REPO is unset.
	// Fork default so online updates track this fork's releases, not upstream.
	defaultUpdateGitHubRepo = "706412584/sub2api"

	// Security: allowed download domains for updates
	allowedDownloadHost = "github.com"
	allowedAssetHost    = "objects.githubusercontent.com"

	// Security: max download size (500MB)
	maxDownloadSize = 500 * 1024 * 1024

	// Rollback: expose at most the 3 most recent versions older than current
	maxRollbackVersions = 3
	// Fetch a few extra releases so filtering (current/newer/prerelease) still leaves enough candidates
	rollbackFetchPageSize = 15

	// Windows binary delta (hdiff): only use patch when clearly smaller than full zip.
	// P0 (0.1.165→0.1.167) measured ~18%; keep a conservative gate for worse diffs.
	windowsPatchSizeRatioThreshold = 0.5
)

// errWindowsPatchUnavailable means the patch path is not applicable; callers fall back to full package.
var errWindowsPatchUnavailable = errors.New("windows patch unavailable")

// UpdateCache defines cache operations for update service
type UpdateCache interface {
	GetUpdateInfo(ctx context.Context) (string, error)
	SetUpdateInfo(ctx context.Context, data string, ttl time.Duration) error
}

// GitHubReleaseClient 获取 GitHub release 信息的接口
type GitHubReleaseClient interface {
	FetchLatestRelease(ctx context.Context, repo string) (*GitHubRelease, error)
	FetchRecentReleases(ctx context.Context, repo string, perPage int) ([]*GitHubRelease, error)
	DownloadFile(ctx context.Context, url, dest string, maxSize int64) error
	FetchChecksumFile(ctx context.Context, url string) ([]byte, error)
}

// GitHubReleaseClientBuilder builds a GitHub release client for a proxy URL.
// Empty proxyURL means direct connection (or env-less client without managed proxy).
type GitHubReleaseClientBuilder func(proxyURL string) GitHubReleaseClient

// UpdateService handles software updates
type UpdateService struct {
	cache          UpdateCache
	githubClient   GitHubReleaseClient
	currentVersion string
	buildType      string // "source" for manual builds, "release" for CI builds
	githubRepo     string // owner/name, from UPDATE_GITHUB_REPO or defaultUpdateGitHubRepo
	proxyRepo      ProxyRepository
	buildClient    GitHubReleaseClientBuilder
}

// NewUpdateService creates a new UpdateService
func NewUpdateService(cache UpdateCache, githubClient GitHubReleaseClient, version, buildType string) *UpdateService {
	return &UpdateService{
		cache:          cache,
		githubClient:   githubClient,
		currentVersion: version,
		buildType:      buildType,
		githubRepo:     resolveUpdateGitHubRepo(),
	}
}

// SetProxyRetry enables one automatic retry via an IP-management active proxy
// when the primary update download hits a network error.
func (s *UpdateService) SetProxyRetry(proxyRepo ProxyRepository, buildClient GitHubReleaseClientBuilder) {
	if s == nil {
		return
	}
	s.proxyRepo = proxyRepo
	s.buildClient = buildClient
}

// resolveUpdateGitHubRepo returns owner/name for release checks.
// Env UPDATE_GITHUB_REPO overrides the fork default (e.g. Wei-Shaw/sub2api).
func resolveUpdateGitHubRepo() string {
	repo := strings.TrimSpace(os.Getenv("UPDATE_GITHUB_REPO"))
	if repo == "" {
		return defaultUpdateGitHubRepo
	}
	// Accept owner/name only; strip accidental github.com prefixes.
	repo = strings.TrimPrefix(repo, "https://github.com/")
	repo = strings.TrimPrefix(repo, "http://github.com/")
	repo = strings.Trim(repo, "/")
	if strings.Count(repo, "/") != 1 || strings.Contains(repo, " ") {
		return defaultUpdateGitHubRepo
	}
	return repo
}

// UpdateInfo contains update information
type UpdateInfo struct {
	CurrentVersion string       `json:"current_version"`
	LatestVersion  string       `json:"latest_version"`
	HasUpdate      bool         `json:"has_update"`
	ReleaseInfo    *ReleaseInfo `json:"release_info,omitempty"`
	Cached         bool         `json:"cached"`
	Warning        string       `json:"warning,omitempty"`
	BuildType      string       `json:"build_type"` // "source" or "release"
}

// ReleaseInfo contains GitHub release details
type ReleaseInfo struct {
	Name        string  `json:"name"`
	Body        string  `json:"body"`
	PublishedAt string  `json:"published_at"`
	HTMLURL     string  `json:"html_url"`
	Assets      []Asset `json:"assets,omitempty"`
}

// Asset represents a release asset
type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
	Size        int64  `json:"size"`
}

// GitHubRelease represents GitHub API response
type GitHubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	PublishedAt string        `json:"published_at"`
	HTMLURL     string        `json:"html_url"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	Assets      []GitHubAsset `json:"assets"`
}

// RollbackVersion describes a release version the system can roll back to
type RollbackVersion struct {
	Version     string `json:"version"` // without "v" prefix, e.g. "0.1.146"
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
}

type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// CheckUpdate checks for available updates
func (s *UpdateService) CheckUpdate(ctx context.Context, force bool) (*UpdateInfo, error) {
	// Try cache first
	if !force {
		if cached, err := s.getFromCache(ctx); err == nil && cached != nil {
			return cached, nil
		}
	}

	// Fetch from GitHub
	info, err := s.fetchLatestRelease(ctx)
	if err != nil {
		// Return cached on error
		if cached, cacheErr := s.getFromCache(ctx); cacheErr == nil && cached != nil {
			cached.Warning = "Using cached data: " + err.Error()
			return cached, nil
		}
		return &UpdateInfo{
			CurrentVersion: s.currentVersion,
			LatestVersion:  s.currentVersion,
			HasUpdate:      false,
			Warning:        err.Error(),
			BuildType:      s.buildType,
		}, nil
	}

	// Cache result
	s.saveToCache(ctx, info)
	return info, nil
}

// PerformUpdate downloads and applies the update
// Uses atomic file replacement pattern for safe in-place updates
func (s *UpdateService) PerformUpdate(ctx context.Context) error {
	info, err := s.CheckUpdate(ctx, true)
	if err != nil {
		return err
	}

	if !info.HasUpdate {
		return ErrNoUpdateAvailable
	}
	if info.ReleaseInfo == nil {
		return fmt.Errorf("missing release info")
	}

	// Windows amd64: prefer N→latest hdiff patch when base hash + size gate pass.
	// Any patch failure falls through to the full archive path.
	if err := s.tryApplyWindowsPatch(ctx, info); err == nil {
		return nil
	}

	return s.applyReleaseAssets(ctx, info.ReleaseInfo.Assets)
}

// applyReleaseAssets downloads the platform archive from the given release assets,
// verifies its checksum, and atomically swaps the running binary.
// Shared by PerformUpdate (latest) and RollbackToVersion (specific older version).
func (s *UpdateService) applyReleaseAssets(ctx context.Context, releaseAssets []Asset) error {
	// Find matching archive and checksum for current platform
	archiveName := s.getArchiveName()
	var downloadURL string
	var checksumURL string

	for _, asset := range releaseAssets {
		if asset.Name == "checksums.txt" {
			checksumURL = asset.DownloadURL
			continue
		}
		if isPlatformFullArchive(asset.Name, archiveName) {
			downloadURL = asset.DownloadURL
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("no compatible release found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// SECURITY: Validate download URL is from trusted domain
	if err := validateDownloadURL(downloadURL); err != nil {
		return fmt.Errorf("invalid download URL: %w", err)
	}
	if checksumURL != "" {
		if err := validateDownloadURL(checksumURL); err != nil {
			return fmt.Errorf("invalid checksum URL: %w", err)
		}
	}

	// Get current executable path
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	exeDir := filepath.Dir(exePath)

	// Create temp directory in the SAME directory as executable
	// This ensures os.Rename is atomic (same filesystem)
	tempDir, err := os.MkdirTemp(exeDir, ".sub2api-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Download archive (network failures retry once via IP-management proxy)
	archivePath := filepath.Join(tempDir, filepath.Base(downloadURL))
	if err := s.downloadFileWithProxyRetry(ctx, downloadURL, archivePath); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	// Verify checksum if available
	if checksumURL != "" {
		if err := s.verifyChecksumWithProxyRetry(ctx, archivePath, checksumURL); err != nil {
			return fmt.Errorf("checksum verification failed: %w", err)
		}
	}

	// Extract binary from archive
	newBinaryPath := filepath.Join(tempDir, "sub2api")
	if err := s.extractBinary(archivePath, newBinaryPath); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	// Set executable permission before replacement
	if err := os.Chmod(newBinaryPath, 0755); err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}

	return replaceExecutableAtomically(exePath, newBinaryPath)
}

// isPlatformFullArchive reports whether name is the full platform release archive
// (zip/tar.gz), excluding patch/sidecar assets that also contain os_arch tokens.
func isPlatformFullArchive(name, archiveName string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || !strings.Contains(lower, strings.ToLower(archiveName)) {
		return false
	}
	switch {
	case strings.HasSuffix(lower, ".txt"),
		strings.HasSuffix(lower, ".hdiff"),
		strings.HasSuffix(lower, ".patch.json"),
		strings.Contains(lower, "hpatchz"),
		strings.Contains(lower, "windows-patch-checksums"):
		return false
	}
	return strings.HasSuffix(lower, ".zip") ||
		strings.HasSuffix(lower, ".tar.gz") ||
		strings.HasSuffix(lower, ".tgz") ||
		strings.HasSuffix(lower, ".tar")
}

// replaceExecutableAtomically renames current -> .backup, then new -> current.
// On failure of the second step it restores the backup.
func replaceExecutableAtomically(exePath, newBinaryPath string) error {
	backupPath := exePath + ".backup"

	// Remove old backup if exists
	_ = os.Remove(backupPath)

	// Step 1: Move current binary to backup
	if err := os.Rename(exePath, backupPath); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	// Step 2: Move new binary to target location (atomic, same filesystem)
	if err := os.Rename(newBinaryPath, exePath); err != nil {
		// Restore backup on failure
		if restoreErr := os.Rename(backupPath, exePath); restoreErr != nil {
			return fmt.Errorf("replace failed and restore failed: %w (restore error: %v)", err, restoreErr)
		}
		return fmt.Errorf("replace failed (restored backup): %w", err)
	}

	// Success - backup file is kept for rollback capability
	// It will be cleaned up on next successful update
	return nil
}

// windowsPatchMeta is the JSON sidecar uploaded by .github/scripts/generate-windows-hdiff-patch.sh
type windowsPatchMeta struct {
	From         string `json:"from"`
	To           string `json:"to"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	BaseSHA256   string `json:"base_sha256"`
	ResultSHA256 string `json:"result_sha256"`
	PatchSize    int64  `json:"patch_size"`
	FullSize     int64  `json:"full_size"`
	Tool         string `json:"tool"`
	ToolVersion  string `json:"tool_version"`
	HPatchzAsset string `json:"hpatchz_asset"`
	PatchAsset   string `json:"patch_asset"`
}

// tryApplyWindowsPatch attempts an N→latest hdiff update on windows/amd64.
// Returns nil on success. Returns errWindowsPatchUnavailable (or any error)
// when the patch path is not used; callers fall back to full package.
func (s *UpdateService) tryApplyWindowsPatch(ctx context.Context, info *UpdateInfo) error {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return errWindowsPatchUnavailable
	}
	if info == nil || info.ReleaseInfo == nil || len(info.ReleaseInfo.Assets) == 0 {
		return errWindowsPatchUnavailable
	}

	from := strings.TrimPrefix(strings.TrimSpace(s.currentVersion), "v")
	to := strings.TrimPrefix(strings.TrimSpace(info.LatestVersion), "v")
	if from == "" || to == "" || from == to {
		return errWindowsPatchUnavailable
	}

	metaAsset, patchAsset, hpatchAsset, fullAsset := findWindowsPatchAssets(info.ReleaseInfo.Assets, from, to)
	if metaAsset == nil || patchAsset == nil || hpatchAsset == nil || fullAsset == nil {
		return errWindowsPatchUnavailable
	}

	// Size gate before downloading anything heavy: prefer meta sizes, else asset sizes.
	// Download meta first (tiny) for authoritative hashes + sizes.
	exePath, err := os.Executable()
	if err != nil {
		return errWindowsPatchUnavailable
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	exeDir := filepath.Dir(exePath)

	tempDir, err := os.MkdirTemp(exeDir, ".sub2api-update-*")
	if err != nil {
		return errWindowsPatchUnavailable
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	metaPath := filepath.Join(tempDir, filepath.Base(metaAsset.Name))
	if err := s.downloadFileWithProxyRetry(ctx, metaAsset.DownloadURL, metaPath); err != nil {
		return errWindowsPatchUnavailable
	}
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		return errWindowsPatchUnavailable
	}
	var meta windowsPatchMeta
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return errWindowsPatchUnavailable
	}
	if !windowsPatchMetaMatches(meta, from, to) {
		return errWindowsPatchUnavailable
	}

	patchSize := meta.PatchSize
	if patchSize <= 0 {
		patchSize = patchAsset.Size
	}
	fullSize := meta.FullSize
	if fullSize <= 0 {
		fullSize = fullAsset.Size
	}
	if fullSize <= 0 || patchSize <= 0 || float64(patchSize) >= float64(fullSize)*windowsPatchSizeRatioThreshold {
		return errWindowsPatchUnavailable
	}

	baseHash, err := fileSHA256Hex(exePath)
	if err != nil || !strings.EqualFold(baseHash, meta.BaseSHA256) {
		// Local binary is not the expected base (manual replace / skipped versions).
		return errWindowsPatchUnavailable
	}

	for _, a := range []*Asset{metaAsset, patchAsset, hpatchAsset} {
		if err := validateDownloadURL(a.DownloadURL); err != nil {
			return errWindowsPatchUnavailable
		}
	}

	patchPath := filepath.Join(tempDir, filepath.Base(patchAsset.Name))
	if err := s.downloadFileWithProxyRetry(ctx, patchAsset.DownloadURL, patchPath); err != nil {
		return fmt.Errorf("patch download failed: %w", err)
	}
	hpatchPath := filepath.Join(tempDir, "hpatchz.exe")
	if err := s.downloadFileWithProxyRetry(ctx, hpatchAsset.DownloadURL, hpatchPath); err != nil {
		return fmt.Errorf("hpatchz download failed: %w", err)
	}

	// Copy current exe as immutable base input (Windows may lock the running image for some ops).
	baseCopy := filepath.Join(tempDir, "base.exe")
	if err := copyFile(exePath, baseCopy); err != nil {
		return errWindowsPatchUnavailable
	}
	outPath := filepath.Join(tempDir, "sub2api.new.exe")
	if err := runHPatchz(ctx, hpatchPath, baseCopy, patchPath, outPath); err != nil {
		return fmt.Errorf("hpatch failed: %w", err)
	}

	outHash, err := fileSHA256Hex(outPath)
	if err != nil || !strings.EqualFold(outHash, meta.ResultSHA256) {
		return fmt.Errorf("patched binary checksum mismatch")
	}
	if err := os.Chmod(outPath, 0755); err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}
	return replaceExecutableAtomically(exePath, outPath)
}

func windowsPatchMetaMatches(meta windowsPatchMeta, from, to string) bool {
	if !strings.EqualFold(strings.TrimSpace(meta.OS), "windows") {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(meta.Arch), "amd64") {
		return false
	}
	if strings.TrimPrefix(strings.TrimSpace(meta.From), "v") != from {
		return false
	}
	if strings.TrimPrefix(strings.TrimSpace(meta.To), "v") != to {
		return false
	}
	if strings.TrimSpace(meta.BaseSHA256) == "" || strings.TrimSpace(meta.ResultSHA256) == "" {
		return false
	}
	return true
}

// findWindowsPatchAssets locates meta/patch/hpatchz/full zip for from→to on windows_amd64.
func findWindowsPatchAssets(assets []Asset, from, to string) (meta, patch, hpatch, full *Asset) {
	wantMeta := fmt.Sprintf("sub2api_%s_to_%s_windows_amd64.patch.json", from, to)
	wantPatch := fmt.Sprintf("sub2api_%s_to_%s_windows_amd64.hdiff", from, to)
	wantHPatch := "hpatchz_windows_amd64.exe"
	archiveToken := "windows_amd64"

	for i := range assets {
		a := &assets[i]
		name := a.Name
		switch {
		case name == wantMeta || strings.HasSuffix(name, wantMeta):
			meta = a
		case name == wantPatch || strings.HasSuffix(name, wantPatch):
			patch = a
		case name == wantHPatch || strings.EqualFold(name, wantHPatch):
			hpatch = a
		case isPlatformFullArchive(name, archiveToken):
			full = a
		}
	}
	return meta, patch, hpatch, full
}

func fileSHA256Hex(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func runHPatchz(ctx context.Context, hpatchzPath, oldPath, patchPath, outPath string) error {
	cmd := exec.CommandContext(ctx, hpatchzPath, oldPath, patchPath, outPath)
	// Avoid flashing a console window on Windows when possible; SysProcAttr is platform-specific
	// and left default here for portability across GOOS build tags.
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	if _, err := os.Stat(outPath); err != nil {
		return fmt.Errorf("hpatchz produced no output: %w", err)
	}
	return nil
}

// Rollback restores the previous version
func (s *UpdateService) Rollback() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	backupFile := exePath + ".backup"
	if _, err := os.Stat(backupFile); os.IsNotExist(err) {
		return fmt.Errorf("no backup found")
	}

	// Replace current with backup
	if err := os.Rename(backupFile, exePath); err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}

	return nil
}

// ListRollbackVersions returns up to maxRollbackVersions release versions that are
// strictly older than the current version (the current version itself is excluded),
// newest first. Draft and prerelease entries are skipped.
func (s *UpdateService) ListRollbackVersions(ctx context.Context) ([]RollbackVersion, error) {
	releases, err := s.fetchRollbackCandidates(ctx)
	if err != nil {
		return nil, err
	}

	versions := make([]RollbackVersion, 0, len(releases))
	for _, r := range releases {
		versions = append(versions, RollbackVersion{
			Version:     strings.TrimPrefix(r.TagName, "v"),
			PublishedAt: r.PublishedAt,
			HTMLURL:     r.HTMLURL,
		})
	}
	return versions, nil
}

// RollbackToVersion downloads and installs a specific older version.
// The target must be one of the versions returned by ListRollbackVersions;
// anything else (including the current version) is rejected.
func (s *UpdateService) RollbackToVersion(ctx context.Context, version string) error {
	target := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if target == "" {
		return ErrRollbackVersionNotAllowed
	}

	releases, err := s.fetchRollbackCandidates(ctx)
	if err != nil {
		return err
	}

	var match *GitHubRelease
	for _, r := range releases {
		if strings.TrimPrefix(r.TagName, "v") == target {
			match = r
			break
		}
	}
	if match == nil {
		return ErrRollbackVersionNotAllowed
	}

	assets := make([]Asset, len(match.Assets))
	for i, a := range match.Assets {
		assets[i] = Asset{
			Name:        a.Name,
			DownloadURL: a.BrowserDownloadURL,
			Size:        a.Size,
		}
	}

	return s.applyReleaseAssets(ctx, assets)
}

// fetchRollbackCandidates fetches recent releases and keeps the newest
// maxRollbackVersions entries strictly older than the current version.
func (s *UpdateService) fetchRollbackCandidates(ctx context.Context) ([]*GitHubRelease, error) {
	releases, err := s.githubClient.FetchRecentReleases(ctx, s.githubRepo, rollbackFetchPageSize)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(releases))
	candidates := make([]*GitHubRelease, 0, maxRollbackVersions)
	for _, r := range releases {
		if r == nil || r.Draft || r.Prerelease {
			continue
		}
		v := strings.TrimPrefix(r.TagName, "v")
		if v == "" || seen[v] {
			continue
		}
		// Only versions strictly older than current (also excludes current itself)
		if compareVersions(v, s.currentVersion) >= 0 {
			continue
		}
		seen[v] = true
		candidates = append(candidates, r)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return compareVersions(
			strings.TrimPrefix(candidates[i].TagName, "v"),
			strings.TrimPrefix(candidates[j].TagName, "v"),
		) > 0
	})

	if len(candidates) > maxRollbackVersions {
		candidates = candidates[:maxRollbackVersions]
	}
	return candidates, nil
}

func (s *UpdateService) fetchLatestRelease(ctx context.Context) (*UpdateInfo, error) {
	release, err := s.githubClient.FetchLatestRelease(ctx, s.githubRepo)
	if err != nil {
		return nil, err
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")

	assets := make([]Asset, len(release.Assets))
	for i, a := range release.Assets {
		assets[i] = Asset{
			Name:        a.Name,
			DownloadURL: a.BrowserDownloadURL,
			Size:        a.Size,
		}
	}

	return &UpdateInfo{
		CurrentVersion: s.currentVersion,
		LatestVersion:  latestVersion,
		HasUpdate:      compareVersions(s.currentVersion, latestVersion) < 0,
		ReleaseInfo: &ReleaseInfo{
			Name:        release.Name,
			Body:        release.Body,
			PublishedAt: release.PublishedAt,
			HTMLURL:     release.HTMLURL,
			Assets:      assets,
		},
		Cached:    false,
		BuildType: s.buildType,
	}, nil
}

func (s *UpdateService) downloadFile(ctx context.Context, downloadURL, dest string) error {
	return s.githubClient.DownloadFile(ctx, downloadURL, dest, maxDownloadSize)
}

func (s *UpdateService) downloadFileWithProxyRetry(ctx context.Context, downloadURL, dest string) error {
	err := s.downloadFile(ctx, downloadURL, dest)
	if err == nil || !isUpdateNetworkError(err) {
		return err
	}
	retryClient, proxyURL, retryErr := s.clientForManagedProxyRetry(ctx)
	if retryErr != nil {
		return err
	}
	if retryErr := retryClient.DownloadFile(ctx, downloadURL, dest, maxDownloadSize); retryErr != nil {
		return fmt.Errorf("%w (proxy retry via %s failed: %v)", err, sanitizeProxyURLForLog(proxyURL), retryErr)
	}
	return nil
}

func (s *UpdateService) verifyChecksumWithProxyRetry(ctx context.Context, filePath, checksumURL string) error {
	err := s.verifyChecksum(ctx, filePath, checksumURL)
	if err == nil || !isUpdateNetworkError(err) {
		return err
	}
	// verifyChecksum uses s.githubClient; temporarily swap for one managed-proxy retry.
	retryClient, proxyURL, retryErr := s.clientForManagedProxyRetry(ctx)
	if retryErr != nil {
		return err
	}
	original := s.githubClient
	s.githubClient = retryClient
	defer func() { s.githubClient = original }()
	if retryErr := s.verifyChecksum(ctx, filePath, checksumURL); retryErr != nil {
		return fmt.Errorf("%w (proxy retry via %s failed: %v)", err, sanitizeProxyURLForLog(proxyURL), retryErr)
	}
	return nil
}

func (s *UpdateService) clientForManagedProxyRetry(ctx context.Context) (GitHubReleaseClient, string, error) {
	if s == nil || s.proxyRepo == nil || s.buildClient == nil {
		return nil, "", fmt.Errorf("managed proxy retry unavailable")
	}
	proxies, err := s.proxyRepo.ListActive(ctx)
	if err != nil {
		return nil, "", err
	}
	proxyURL := pickManagedProxyURL(proxies)
	if proxyURL == "" {
		return nil, "", fmt.Errorf("no active managed proxy")
	}
	client := s.buildClient(proxyURL)
	if client == nil {
		return nil, "", fmt.Errorf("failed to build proxy client")
	}
	return client, proxyURL, nil
}

func pickManagedProxyURL(proxies []Proxy) string {
	if len(proxies) == 0 {
		return ""
	}
	// Prefer local HTTP proxies first (common for GitHub access), then any active entry.
	for _, p := range proxies {
		if !p.IsActive() {
			continue
		}
		proto := strings.ToLower(strings.TrimSpace(p.Protocol))
		if (proto == "http" || proto == "https") && (p.Host == "127.0.0.1" || p.Host == "localhost") {
			return p.URL()
		}
	}
	for _, p := range proxies {
		if p.IsActive() {
			return p.URL()
		}
	}
	return ""
}

func isUpdateNetworkError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, token := range []string{
		"timeout",
		"i/o timeout",
		"connection refused",
		"connection reset",
		"tls handshake timeout",
		"no such host",
		"temporary failure",
		"network is unreachable",
		"dial tcp",
		"proxyconnect",
		"eof",
	} {
		if strings.Contains(msg, token) {
			return true
		}
	}
	return false
}

func sanitizeProxyURLForLog(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "managed-proxy"
	}
	u.User = nil
	return u.Scheme + "://" + u.Host
}

func (s *UpdateService) getArchiveName() string {
	osName := runtime.GOOS
	arch := runtime.GOARCH
	return fmt.Sprintf("%s_%s", osName, arch)
}

// validateDownloadURL checks if the URL is from an allowed domain
// SECURITY: This prevents SSRF and ensures downloads only come from trusted GitHub domains
func validateDownloadURL(rawURL string) error {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Must be HTTPS
	if parsedURL.Scheme != "https" {
		return fmt.Errorf("only HTTPS URLs are allowed")
	}

	// Check against allowed hosts
	host := parsedURL.Host
	// GitHub release URLs can be from github.com or objects.githubusercontent.com
	if host != allowedDownloadHost &&
		!strings.HasSuffix(host, "."+allowedDownloadHost) &&
		host != allowedAssetHost &&
		!strings.HasSuffix(host, "."+allowedAssetHost) {
		return fmt.Errorf("download from untrusted host: %s", host)
	}

	return nil
}

func (s *UpdateService) verifyChecksum(ctx context.Context, filePath, checksumURL string) error {
	// Download checksums file
	checksumData, err := s.githubClient.FetchChecksumFile(ctx, checksumURL)
	if err != nil {
		return fmt.Errorf("failed to download checksums: %w", err)
	}

	// Calculate file hash
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actualHash := hex.EncodeToString(h.Sum(nil))

	// Find expected hash in checksums file
	fileName := filepath.Base(filePath)
	scanner := bufio.NewScanner(strings.NewReader(string(checksumData)))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == fileName {
			if parts[0] == actualHash {
				return nil
			}
			return fmt.Errorf("checksum mismatch: expected %s, got %s", parts[0], actualHash)
		}
	}

	return fmt.Errorf("checksum not found for %s", fileName)
}

func (s *UpdateService) extractBinary(archivePath, destPath string) error {
	const maxBinarySize = 500 * 1024 * 1024

	// Windows release assets are zip; Linux/macOS use tar.gz.
	if strings.HasSuffix(strings.ToLower(archivePath), ".zip") {
		return extractBinaryFromZip(archivePath, destPath, maxBinarySize)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	var reader io.Reader = f

	// Handle gzip compression
	if strings.HasSuffix(archivePath, ".gz") || strings.HasSuffix(archivePath, ".tar.gz") || strings.HasSuffix(archivePath, ".tgz") {
		gzr, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer func() { _ = gzr.Close() }()
		reader = gzr
	}

	// Handle tar archive
	if strings.Contains(archivePath, ".tar") {
		tr := tar.NewReader(reader)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}

			// SECURITY: Prevent Zip Slip / Path Traversal attack
			// Only allow files with safe base names, no directory traversal
			baseName := filepath.Base(hdr.Name)

			// Check for path traversal attempts
			if strings.Contains(hdr.Name, "..") {
				return fmt.Errorf("path traversal attempt detected: %s", hdr.Name)
			}

			// Validate the entry is a regular file
			if hdr.Typeflag != tar.TypeReg {
				continue // Skip directories and special files
			}

			// Only extract the specific binary we need
			if baseName == "sub2api" || baseName == "sub2api.exe" {
				if hdr.Size > maxBinarySize {
					return fmt.Errorf("binary too large: %d bytes (max %d)", hdr.Size, maxBinarySize)
				}

				out, err := os.Create(destPath)
				if err != nil {
					return err
				}

				// Use LimitReader to prevent decompression bombs
				limited := io.LimitReader(tr, maxBinarySize)
				if _, err := io.Copy(out, limited); err != nil {
					_ = out.Close()
					return err
				}
				if err := out.Close(); err != nil {
					return err
				}
				return nil
			}
		}
		return fmt.Errorf("binary not found in archive")
	}

	// Direct copy for non-tar files (with size limit)
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}

	limited := io.LimitReader(reader, maxBinarySize)
	if _, err := io.Copy(out, limited); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func extractBinaryFromZip(archivePath, destPath string, maxBinarySize int64) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()

	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		// SECURITY: Prevent Zip Slip / Path Traversal attack
		if strings.Contains(entry.Name, "..") {
			return fmt.Errorf("path traversal attempt detected: %s", entry.Name)
		}
		baseName := filepath.Base(entry.Name)
		if baseName != "sub2api" && baseName != "sub2api.exe" {
			continue
		}
		if int64(entry.UncompressedSize64) > maxBinarySize {
			return fmt.Errorf("binary too large: %d bytes (max %d)", entry.UncompressedSize64, maxBinarySize)
		}
		rc, err := entry.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(destPath)
		if err != nil {
			_ = rc.Close()
			return err
		}
		limited := io.LimitReader(rc, maxBinarySize)
		_, copyErr := io.Copy(out, limited)
		closeOutErr := out.Close()
		closeRCErr := rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
		if closeRCErr != nil {
			return closeRCErr
		}
		return nil
	}
	return fmt.Errorf("binary not found in archive")
}

func (s *UpdateService) getFromCache(ctx context.Context) (*UpdateInfo, error) {
	data, err := s.cache.GetUpdateInfo(ctx)
	if err != nil {
		return nil, err
	}

	var cached struct {
		Latest      string       `json:"latest"`
		ReleaseInfo *ReleaseInfo `json:"release_info"`
		Timestamp   int64        `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(data), &cached); err != nil {
		return nil, err
	}

	if time.Now().Unix()-cached.Timestamp > updateCacheTTL {
		return nil, fmt.Errorf("cache expired")
	}

	return &UpdateInfo{
		CurrentVersion: s.currentVersion,
		LatestVersion:  cached.Latest,
		HasUpdate:      compareVersions(s.currentVersion, cached.Latest) < 0,
		ReleaseInfo:    cached.ReleaseInfo,
		Cached:         true,
		BuildType:      s.buildType,
	}, nil
}

func (s *UpdateService) saveToCache(ctx context.Context, info *UpdateInfo) {
	cacheData := struct {
		Latest      string       `json:"latest"`
		ReleaseInfo *ReleaseInfo `json:"release_info"`
		Timestamp   int64        `json:"timestamp"`
	}{
		Latest:      info.LatestVersion,
		ReleaseInfo: info.ReleaseInfo,
		Timestamp:   time.Now().Unix(),
	}

	data, _ := json.Marshal(cacheData)
	_ = s.cache.SetUpdateInfo(ctx, string(data), time.Duration(updateCacheTTL)*time.Second)
}

// compareVersions compares two semantic versions
func compareVersions(current, latest string) int {
	currentParts := parseVersion(current)
	latestParts := parseVersion(latest)

	for i := 0; i < 3; i++ {
		if currentParts[i] < latestParts[i] {
			return -1
		}
		if currentParts[i] > latestParts[i] {
			return 1
		}
	}
	return 0
}

func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	result := [3]int{0, 0, 0}
	for i := 0; i < len(parts) && i < 3; i++ {
		if parsed, err := strconv.Atoi(parts[i]); err == nil {
			result[i] = parsed
		}
	}
	return result
}

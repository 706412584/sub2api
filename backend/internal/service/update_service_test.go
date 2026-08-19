//go:build unit

package service

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type updateServiceCacheStub struct {
	data string
}

func (s *updateServiceCacheStub) GetUpdateInfo(context.Context) (string, error) {
	if s.data == "" {
		return "", errors.New("cache miss")
	}
	return s.data, nil
}

func (s *updateServiceCacheStub) SetUpdateInfo(_ context.Context, data string, _ time.Duration) error {
	s.data = data
	return nil
}

type updateServiceGitHubClientStub struct {
	release        *GitHubRelease
	recentReleases []*GitHubRelease
	recentErr      error
	lastRepo       string
}

func (s *updateServiceGitHubClientStub) FetchLatestRelease(_ context.Context, repo string) (*GitHubRelease, error) {
	s.lastRepo = repo
	return s.release, nil
}

func (s *updateServiceGitHubClientStub) FetchRecentReleases(_ context.Context, repo string, _ int) ([]*GitHubRelease, error) {
	s.lastRepo = repo
	return s.recentReleases, s.recentErr
}

func (s *updateServiceGitHubClientStub) DownloadFile(context.Context, string, string, int64) error {
	panic("DownloadFile should not be called when no update is available")
}

func (s *updateServiceGitHubClientStub) FetchChecksumFile(context.Context, string) ([]byte, error) {
	panic("FetchChecksumFile should not be called when no update is available")
}

func TestResolveUpdateGitHubRepoDefaultAndOverride(t *testing.T) {
	t.Setenv("UPDATE_GITHUB_REPO", "")
	require.Equal(t, defaultUpdateGitHubRepo, resolveUpdateGitHubRepo())

	t.Setenv("UPDATE_GITHUB_REPO", "Wei-Shaw/sub2api")
	require.Equal(t, "Wei-Shaw/sub2api", resolveUpdateGitHubRepo())

	t.Setenv("UPDATE_GITHUB_REPO", "https://github.com/706412584/sub2api/")
	require.Equal(t, "706412584/sub2api", resolveUpdateGitHubRepo())

	t.Setenv("UPDATE_GITHUB_REPO", "not-a-repo")
	require.Equal(t, defaultUpdateGitHubRepo, resolveUpdateGitHubRepo())
}

func TestUpdateServiceUsesConfiguredGitHubRepo(t *testing.T) {
	t.Setenv("UPDATE_GITHUB_REPO", "example-owner/example-repo")
	client := &updateServiceGitHubClientStub{
		release: &GitHubRelease{TagName: "v0.1.200", Name: "v0.1.200"},
	}
	svc := NewUpdateService(&updateServiceCacheStub{}, client, "0.1.100", "release")

	info, err := svc.CheckUpdate(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, "0.1.200", info.LatestVersion)
	require.True(t, info.HasUpdate)
	require.Equal(t, "example-owner/example-repo", client.lastRepo)
}

func TestUpdateServicePerformUpdateNoUpdateReturnsSentinel(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{
			release: &GitHubRelease{
				TagName: "v0.1.132",
				Name:    "v0.1.132",
			},
		},
		"0.1.132",
		"release",
	)

	err := svc.PerformUpdate(context.Background())

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoUpdateAvailable))
	require.ErrorIs(t, err, ErrNoUpdateAvailable)
}

func newRollbackTestService(current string, releases []*GitHubRelease) *UpdateService {
	return NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentReleases: releases},
		current,
		"release",
	)
}

func TestUpdateServiceListRollbackVersionsFiltersAndCaps(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148", PublishedAt: "2026-07-09T00:00:00Z"},                       // newer than current: excluded
		{TagName: "v0.1.147", PublishedAt: "2026-07-08T00:00:00Z"},                       // current: excluded
		{TagName: "v0.1.146-rc1", PublishedAt: "2026-07-07T12:00:00Z", Prerelease: true}, // prerelease: excluded
		{TagName: "v0.1.146", PublishedAt: "2026-07-07T00:00:00Z"},
		{TagName: "v0.1.145", PublishedAt: "2026-07-06T00:00:00Z", Draft: true}, // draft: excluded
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"},
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"}, // duplicate: excluded
		{TagName: "v0.1.143", PublishedAt: "2026-07-04T00:00:00Z"},
		{TagName: "v0.1.142", PublishedAt: "2026-07-03T00:00:00Z"}, // beyond cap of 3: excluded
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.144", versions[1].Version)
	require.Equal(t, "0.1.143", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsSortsUnorderedInput(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.144"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.145", versions[1].Version)
	require.Equal(t, "0.1.144", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsEmptyWhenNoneOlder(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.148"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Empty(t, versions)
}

func TestUpdateServiceListRollbackVersionsPropagatesFetchError(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentErr: errors.New("github unavailable")},
		"0.1.147",
		"release",
	)

	_, err := svc.ListRollbackVersions(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "github unavailable")
}

func TestUpdateServiceRollbackToVersionRejectsDisallowedTargets(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148"},
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
		{TagName: "v0.1.144"},
		{TagName: "v0.1.143"},
		{TagName: "v0.1.142"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	for _, target := range []string{
		"",         // empty
		"0.1.147",  // current version
		"v0.1.147", // current version with prefix
		"0.1.148",  // newer than current
		"0.1.142",  // older than the 3 most recent
		"9.9.9",    // nonexistent
	} {
		err := svc.RollbackToVersion(context.Background(), target)
		require.ErrorIs(t, err, ErrRollbackVersionNotAllowed, "target %q should be rejected", target)
	}
}

func TestUpdateServiceRollbackToVersionAcceptsVPrefix(t *testing.T) {
	// No platform asset in the release: the target passes the allowlist check
	// and fails later at asset lookup, proving the version itself was accepted.
	releases := []*GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	err := svc.RollbackToVersion(context.Background(), "v0.1.146")

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrRollbackVersionNotAllowed)
	require.Contains(t, err.Error(), "no compatible release found")
}

func TestExtractBinaryFromZipFindsExe(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "sub2api_0.1.166_windows_amd64.zip")
	dest := filepath.Join(dir, "sub2api")

	zf, err := os.Create(zipPath)
	require.NoError(t, err)
	zw := zip.NewWriter(zf)
	w, err := zw.Create("sub2api.exe")
	require.NoError(t, err)
	_, err = w.Write([]byte("MZ-fake-binary"))
	require.NoError(t, err)
	// also add junk file
	_, err = zw.Create("README.md")
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, zf.Close())

	err = extractBinaryFromZip(zipPath, dest, 1024*1024)
	require.NoError(t, err)
	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, "MZ-fake-binary", string(data))
}

func TestIsPlatformFullArchive(t *testing.T) {
	require.True(t, isPlatformFullArchive("sub2api_0.1.167_windows_amd64.zip", "windows_amd64"))
	require.True(t, isPlatformFullArchive("sub2api_0.1.167_linux_amd64.tar.gz", "linux_amd64"))
	require.False(t, isPlatformFullArchive("sub2api_0.1.165_to_0.1.167_windows_amd64.hdiff", "windows_amd64"))
	require.False(t, isPlatformFullArchive("sub2api_0.1.165_to_0.1.167_windows_amd64.patch.json", "windows_amd64"))
	require.False(t, isPlatformFullArchive("hpatchz_windows_amd64.exe", "windows_amd64"))
	require.False(t, isPlatformFullArchive("windows-patch-checksums.txt", "windows_amd64"))
	require.False(t, isPlatformFullArchive("checksums.txt", "windows_amd64"))
}

func TestWindowsPatchMetaMatches(t *testing.T) {
	meta := windowsPatchMeta{
		From: "0.1.165", To: "0.1.167", OS: runtime.GOOS, Arch: runtime.GOARCH,
		BaseSHA256: "aaa", ResultSHA256: "bbb",
	}
	require.True(t, windowsPatchMetaMatches(meta, "0.1.165", "0.1.167"))
	require.False(t, windowsPatchMetaMatches(meta, "0.1.166", "0.1.167"))
	require.False(t, windowsPatchMetaMatches(meta, "0.1.165", "0.1.168"))
	meta.OS = "not-" + runtime.GOOS
	require.False(t, windowsPatchMetaMatches(meta, "0.1.165", "0.1.167"))
}

func TestFindDeltaPatchAssets(t *testing.T) {
	assets := []Asset{
		{Name: "sub2api_0.1.167_windows_amd64.zip", DownloadURL: "https://github.com/x/y/full.zip", Size: 34_000_000},
		{Name: "sub2api_0.1.165_to_0.1.167_windows_amd64.hdiff", DownloadURL: "https://github.com/x/y/p.hdiff", Size: 6_000_000},
		{Name: "sub2api_0.1.165_to_0.1.167_windows_amd64.patch.json", DownloadURL: "https://github.com/x/y/p.json", Size: 400},
		{Name: "hpatchz_windows_amd64.exe", DownloadURL: "https://github.com/x/y/hpatchz.exe", Size: 500_000},
		{Name: "checksums.txt", DownloadURL: "https://github.com/x/y/checksums.txt", Size: 500},
	}
	meta, patch, hpatch, full := findDeltaPatchAssets(assets, "0.1.165", "0.1.167", "windows_amd64")
	require.NotNil(t, meta)
	require.NotNil(t, patch)
	require.NotNil(t, hpatch)
	require.NotNil(t, full)
	require.Equal(t, "sub2api_0.1.167_windows_amd64.zip", full.Name)

	meta, patch, hpatch, full = findDeltaPatchAssets(assets, "0.1.160", "0.1.167", "windows_amd64")
	require.Nil(t, meta)
	require.Nil(t, patch)
	require.NotNil(t, hpatch)
	require.NotNil(t, full)

	// Linux platform token: patch assets must be named linux_amd64 (no .exe hpatchz).
	linuxAssets := []Asset{
		{Name: "sub2api_0.1.167_linux_amd64.tar.gz", DownloadURL: "https://github.com/x/y/linux-full.tar.gz", Size: 34_000_000},
		{Name: "sub2api_0.1.165_to_0.1.167_linux_amd64.hdiff", DownloadURL: "https://github.com/x/y/linux-p.hdiff", Size: 3_000_000},
		{Name: "sub2api_0.1.165_to_0.1.167_linux_amd64.patch.json", DownloadURL: "https://github.com/x/y/linux-p.json", Size: 400},
		{Name: "hpatchz_linux_amd64", DownloadURL: "https://github.com/x/y/hpatchz-linux", Size: 900_000},
		{Name: "checksums.txt", DownloadURL: "https://github.com/x/y/checksums.txt", Size: 500},
	}
	meta, patch, hpatch, full = findDeltaPatchAssets(linuxAssets, "0.1.165", "0.1.167", "linux_amd64")
	require.NotNil(t, meta)
	require.NotNil(t, patch)
	require.NotNil(t, hpatch)
	require.NotNil(t, full)
	require.Equal(t, "sub2api_0.1.167_linux_amd64.tar.gz", full.Name)
	require.Equal(t, "hpatchz_linux_amd64", hpatch.Name)
}

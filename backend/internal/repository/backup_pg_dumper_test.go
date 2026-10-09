//go:build unit

package repository

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolvePostgresCLI_PrefersPGBinDir(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))

	name := "pg_dump"
	if runtime.GOOS == "windows" {
		name = "pg_dump.exe"
	}
	target := filepath.Join(binDir, name)
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o755))

	t.Setenv("PG_BIN_DIR", binDir)
	t.Setenv("PG_DUMP", "")
	got, err := resolvePostgresCLI("pg_dump")
	require.NoError(t, err)
	require.Equal(t, target, got)
}

func TestResolvePostgresCLI_PrefersExplicitPGDump(t *testing.T) {
	dir := t.TempDir()
	name := "pg_dump_custom"
	if runtime.GOOS == "windows" {
		name = "pg_dump_custom.exe"
	}
	target := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o755))

	t.Setenv("PG_DUMP", target)
	t.Setenv("PG_BIN_DIR", "")
	got, err := resolvePostgresCLI("pg_dump")
	require.NoError(t, err)
	require.Equal(t, target, got)
}

func TestResolvePostgresCLI_FindsBesideWorkdir(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "postgres", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	name := "pg_dump"
	if runtime.GOOS == "windows" {
		name = "pg_dump.exe"
	}
	target := filepath.Join(binDir, name)
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o755))

	t.Setenv("PG_DUMP", "")
	t.Setenv("PG_BIN_DIR", "")
	t.Setenv("DATABASE_PG_BIN_DIR", "")
	t.Setenv("POSTGRES_BIN_DIR", "")

	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(wd) })

	got, err := resolvePostgresCLI("pg_dump")
	require.NoError(t, err)
	require.Equal(t, target, got)
}

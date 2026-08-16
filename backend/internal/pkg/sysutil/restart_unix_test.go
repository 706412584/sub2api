//go:build !windows && unit

package sysutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShQuoteEscapesSingleQuotes(t *testing.T) {
	require.Equal(t, `'/etc/sub2api'`, shQuote(`/etc/sub2api`))
	require.Equal(t, `'it'\''s'`, shQuote(`it's`))
}

func TestBuildLinuxRestartScriptKeySteps(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "sub2api")
	require.NoError(t, os.WriteFile(exe, []byte("x"), 0o755))

	script, err := BuildLinuxRestartScript(4242, exe, dir)
	require.NoError(t, err)

	require.Contains(t, script, "TARGET_PID=4242")
	require.Contains(t, script, "update-restart.log")
	require.Contains(t, script, "EXE_BACKUP")
	require.Contains(t, script, "nohup")
	require.Contains(t, script, "127.0.0.1:18080/health")
	require.Contains(t, script, "restoring backup")
	require.Contains(t, script, "mv -f")
	require.Contains(t, script, "wait_pid_gone")
	require.Contains(t, script, "restart and rollback both failed")
	require.Equal(t, `'`+exe+`'`, shQuote(exe))
}

func TestLinuxRestartScriptRestoresBackup(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "sub2api")
	require.NoError(t, os.WriteFile(exe, []byte("x"), 0o755))

	script, err := BuildLinuxRestartScript(99, exe, dir)
	require.NoError(t, err)

	// The rollback branch must reference the backup path next to the exe.
	require.Contains(t, script, exe+".backup")
	require.Contains(t, script, "restart and rollback both failed")
}

func TestWriteLinuxRestartScriptCreatesFile(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "sub2api")
	require.NoError(t, os.WriteFile(exe, []byte("x"), 0o755))

	path, err := writeLinuxRestartScript(1234, exe, dir)
	require.NoError(t, err)
	require.FileExists(t, path)
	require.True(t, strings.HasPrefix(path, filepath.Join(os.TempDir(), "sub2api-restart-")))
	_ = os.Remove(path)
}
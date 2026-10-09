//go:build windows && unit

package sysutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPsQuoteEscapesSingleQuotes(t *testing.T) {
	require.Equal(t, `'C:\path\app.exe'`, psQuote(`C:\path\app.exe`))
	require.Equal(t, `'C:\O''Brien\app.exe'`, psQuote(`C:\O'Brien\app.exe`))
}

func TestBuildWindowsRestartScriptPrefersNativeControl(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "sub2api.exe")
	require.NoError(t, os.WriteFile(exe, []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "native-control.ps1"), []byte("param()"), 0o644))

	script := buildWindowsRestartScript(4242, exe, dir)

	require.Contains(t, script, "$targetPid = 4242")
	require.Contains(t, script, "native-control.ps1")
	require.Contains(t, script, "restart-app")
	require.Contains(t, script, "update-restart.log")
	require.Contains(t, script, "Start-DirectProcess")
	require.Contains(t, script, "Wait-LogHandlesReleased")
	require.Contains(t, script, "runtime-env.json")
	require.Contains(t, script, "Stop-Process -Id $ProcessId -Force")
}

func TestBuildWindowsRestartScriptFallsBackToDirectRelaunch(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "sub2api.exe")
	require.NoError(t, os.WriteFile(exe, []byte("x"), 0o644))

	script := buildWindowsRestartScript(4242, exe, dir)

	require.Contains(t, script, "$targetPid = 4242")
	require.Contains(t, script, "Start-DirectProcess")
	require.Contains(t, script, filepath.Base(exe))
	require.True(t, strings.Contains(script, "sub2api.pid") || strings.Contains(script, `run`))
	require.Contains(t, script, "update-restart.log")
}

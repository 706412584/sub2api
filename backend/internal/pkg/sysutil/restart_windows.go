//go:build windows

package sysutil

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	windowsDetachedProcess       = 0x00000008
	windowsCreateNewProcessGroup = 0x00000200
	windowsCreateNoWindow        = 0x08000000
)

func platformRestart() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	workDir, err := os.Getwd()
	if err != nil || strings.TrimSpace(workDir) == "" {
		workDir = filepath.Dir(exe)
	}

	helperScript := buildWindowsRestartScript(os.Getpid(), exe, workDir)
	cmd := exec.Command("powershell.exe",
		"-NoProfile",
		"-ExecutionPolicy", "Bypass",
		"-WindowStyle", "Hidden",
		"-Command", helperScript,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windowsDetachedProcess | windowsCreateNewProcessGroup | windowsCreateNoWindow,
	}
	// Detach from parent lifetime; do not inherit our stdio handles.
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start windows restart helper: %w", err)
	}
	// Helper outlives this process; release so we do not keep a waitable handle.
	if err := cmd.Process.Release(); err != nil {
		log.Printf("windows restart helper release warning: %v", err)
	}

	log.Println("Initiating Windows service restart via detached helper...")
	log.Printf("Restart helper will relaunch: %s", exe)

	go func() {
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}

// buildWindowsRestartScript waits for the current PID to exit, then starts the
// service again. Prefer native-control.ps1 restart-app (app-only, keeps DB/Redis);
// fall back to start; otherwise re-launch the binary in the same working directory.
func buildWindowsRestartScript(pid int, exe, workDir string) string {
	exeDir := filepath.Dir(exe)
	nativeControl := filepath.Join(exeDir, "native-control.ps1")

	var relaunch string
	if _, err := os.Stat(nativeControl); err == nil {
		// Prefer restart-app (only cycles sub2api). Older scripts without that
		// action fall back to start (safe when the old process has already exited).
		relaunch = fmt.Sprintf(
			`& %s restart-app; if ($LASTEXITCODE -ne 0) { & %s start }; `+
				`if ($LASTEXITCODE -ne 0) { throw "native-control restart/start failed: $LASTEXITCODE" }`,
			psQuote(nativeControl),
			psQuote(nativeControl),
		)
	} else {
		// Best-effort direct relaunch when no control script is present.
		// Also refresh run\sub2api.pid when that local convention exists.
		pidDir := filepath.Join(workDir, "run")
		relaunch = fmt.Sprintf(
			`$proc = Start-Process -FilePath %s -WorkingDirectory %s -WindowStyle Hidden -PassThru; `+
				`$pidDir = %s; if (Test-Path -LiteralPath $pidDir) { `+
				`Set-Content -LiteralPath (Join-Path $pidDir 'sub2api.pid') -Value $proc.Id -Encoding ascii }`,
			psQuote(exe),
			psQuote(workDir),
			psQuote(pidDir),
		)
	}

	// Wait until the old process is gone; force-kill if it ignores graceful exit
	// (stuck handlers). Then give handles a moment to release log file locks.
	return fmt.Sprintf(
		`$ErrorActionPreference = 'Stop'; `+
			`$targetPid = %d; `+
			`for ($i = 0; $i -lt 100; $i++) { `+
			`if (-not (Get-Process -Id $targetPid -ErrorAction SilentlyContinue)) { break }; `+
			`Start-Sleep -Milliseconds 200 `+
			`}; `+
			`if (Get-Process -Id $targetPid -ErrorAction SilentlyContinue) { `+
			`Stop-Process -Id $targetPid -Force -ErrorAction SilentlyContinue; `+
			`Start-Sleep -Milliseconds 500 `+
			`}; `+
			`if (Get-Process -Id $targetPid -ErrorAction SilentlyContinue) { `+
			`throw "timed out waiting for process $targetPid to exit" `+
			`}; `+
			`Start-Sleep -Milliseconds 500; `+
			`%s`,
		pid,
		relaunch,
	)
}

func psQuote(value string) string {
	// PowerShell single-quoted string; escape embedded single quotes by doubling.
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

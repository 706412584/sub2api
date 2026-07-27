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

	// 写入临时 .ps1，避免超长 -Command 被截断，也便于排障。
	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("sub2api-restart-%d.ps1", os.Getpid()))
	if err := os.WriteFile(scriptPath, []byte(helperScript), 0o600); err != nil {
		return fmt.Errorf("write windows restart helper script: %w", err)
	}

	cmd := exec.Command("powershell.exe",
		"-NoProfile",
		"-ExecutionPolicy", "Bypass",
		"-WindowStyle", "Hidden",
		"-File", scriptPath,
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
		_ = os.Remove(scriptPath)
		return fmt.Errorf("start windows restart helper: %w", err)
	}
	// Helper outlives this process; release so we do not keep a waitable handle.
	if err := cmd.Process.Release(); err != nil {
		log.Printf("windows restart helper release warning: %v", err)
	}

	log.Println("Initiating Windows service restart via detached helper...")
	log.Printf("Restart helper script: %s", scriptPath)
	log.Printf("Restart helper will relaunch: %s", exe)

	go func() {
		// 给 HTTP 响应和 helper 拉起留一点时间；过长会让用户感觉“卡住”。
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}

// buildWindowsRestartScript waits for the current PID to exit, then starts the
// service again. Prefer native-control.ps1 restart-app (app-only, keeps DB/Redis);
// fall back to start; otherwise re-launch the binary with runtime-env.json loaded.
//
// 关键点：
//  1. 全程写 logs/update-restart.log（或 %TEMP%），避免静默失败
//  2. 等待旧进程退出 + 日志文件句柄释放，再重试多次启动
//  3. Redirect 到同一 stdout/stderr 失败时，无重定向启动（保证服务起来）
func buildWindowsRestartScript(pid int, exe, workDir string) string {
	exeDir := filepath.Dir(exe)
	nativeControl := filepath.Join(exeDir, "native-control.ps1")
	runtimeEnv := filepath.Join(workDir, "runtime-env.json")
	logDir := filepath.Join(workDir, "logs")
	restartLog := filepath.Join(logDir, "update-restart.log")
	pidFile := filepath.Join(workDir, "run", "sub2api.pid")
	stdoutLog := filepath.Join(logDir, "sub2api.stdout.log")
	stderrLog := filepath.Join(logDir, "sub2api.stderr.log")

	var body strings.Builder
	body.WriteString("$ErrorActionPreference = 'Continue'\n")
	body.WriteString(fmt.Sprintf("$targetPid = %d\n", pid))
	body.WriteString(fmt.Sprintf("$exe = %s\n", psQuote(exe)))
	body.WriteString(fmt.Sprintf("$workDir = %s\n", psQuote(workDir)))
	body.WriteString(fmt.Sprintf("$nativeControl = %s\n", psQuote(nativeControl)))
	body.WriteString(fmt.Sprintf("$runtimeEnv = %s\n", psQuote(runtimeEnv)))
	body.WriteString(fmt.Sprintf("$logDir = %s\n", psQuote(logDir)))
	body.WriteString(fmt.Sprintf("$restartLog = %s\n", psQuote(restartLog)))
	body.WriteString(fmt.Sprintf("$pidFile = %s\n", psQuote(pidFile)))
	body.WriteString(fmt.Sprintf("$stdoutLog = %s\n", psQuote(stdoutLog)))
	body.WriteString(fmt.Sprintf("$stderrLog = %s\n", psQuote(stderrLog)))
	body.WriteString(fmt.Sprintf("$tempLog = Join-Path $env:TEMP ('sub2api-restart-' + %d + '.log')\n", pid))

	body.WriteString(`
function Write-RestartLog([string]$Message) {
  $line = ('[{0}] {1}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Message)
  try {
    if (-not (Test-Path -LiteralPath $logDir)) {
      New-Item -ItemType Directory -Path $logDir -Force | Out-Null
    }
    Add-Content -LiteralPath $restartLog -Value $line -Encoding UTF8 -ErrorAction Stop
  } catch {
    try { Add-Content -LiteralPath $tempLog -Value $line -Encoding UTF8 -ErrorAction SilentlyContinue } catch {}
  }
}

function Test-AppHealth {
  try {
    $resp = Invoke-WebRequest -UseBasicParsing -Uri 'http://127.0.0.1:18080/health' -TimeoutSec 3
    return ($resp.StatusCode -eq 200)
  } catch {
    return $false
  }
}

function Wait-ProcessGone([int]$ProcessId, [int]$TimeoutMs = 30000) {
  $deadline = (Get-Date).AddMilliseconds($TimeoutMs)
  while ((Get-Date) -lt $deadline) {
    if (-not (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)) {
      return $true
    }
    Start-Sleep -Milliseconds 200
  }
  if (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue) {
    Write-RestartLog "force-killing pid $ProcessId"
    Stop-Process -Id $ProcessId -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 800
  }
  return -not (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)
}

function Wait-LogHandlesReleased {
  # 旧进程 Redirect 的日志句柄可能尚未释放，立刻 Start-Process -Redirect 会失败。
  foreach ($path in @($stdoutLog, $stderrLog)) {
    if (-not (Test-Path -LiteralPath $path)) { continue }
    for ($i = 0; $i -lt 20; $i++) {
      try {
        $fs = [System.IO.File]::Open($path, [System.IO.FileMode]::Open, [System.IO.FileAccess]::ReadWrite, [System.IO.FileShare]::Read)
        $fs.Close()
        break
      } catch {
        Start-Sleep -Milliseconds 250
      }
    }
  }
  Start-Sleep -Milliseconds 500
}

function Import-RuntimeEnv {
  if (-not (Test-Path -LiteralPath $runtimeEnv)) {
    Write-RestartLog "runtime-env.json missing: $runtimeEnv"
    return
  }
  try {
    $vars = Get-Content -LiteralPath $runtimeEnv -Raw | ConvertFrom-Json
    foreach ($p in $vars.PSObject.Properties) {
      [Environment]::SetEnvironmentVariable($p.Name, [string]$p.Value, 'Process')
    }
    Write-RestartLog "loaded runtime-env.json"
  } catch {
    Write-RestartLog ("failed to load runtime-env.json: " + $_.Exception.Message)
  }
}

function Start-DirectProcess([bool]$WithRedirect) {
  Import-RuntimeEnv
  $pidDir = Split-Path -Parent $pidFile
  if ($pidDir -and -not (Test-Path -LiteralPath $pidDir)) {
    New-Item -ItemType Directory -Path $pidDir -Force | Out-Null
  }
  if (-not (Test-Path -LiteralPath $logDir)) {
    New-Item -ItemType Directory -Path $logDir -Force | Out-Null
  }

  $startParams = @{
    FilePath         = $exe
    WorkingDirectory = $workDir
    WindowStyle      = 'Hidden'
    PassThru         = $true
  }
  if ($WithRedirect) {
    $startParams.RedirectStandardOutput = $stdoutLog
    $startParams.RedirectStandardError  = $stderrLog
  }

  $proc = Start-Process @startParams
  if (-not $proc) {
    throw "Start-Process returned null"
  }
  if ($pidDir) {
    Set-Content -LiteralPath $pidFile -Value $proc.Id -Encoding ascii
  }
  Write-RestartLog ("started direct process pid=" + $proc.Id + " redirect=" + $WithRedirect)
  return $proc
}

function Start-WithNativeControl {
  if (-not (Test-Path -LiteralPath $nativeControl)) {
    return $false
  }
  Write-RestartLog "trying native-control.ps1 restart-app"
  & $nativeControl restart-app
  $code = $LASTEXITCODE
  Write-RestartLog ("native-control restart-app exit=" + $code)
  if ($code -eq 0 -and (Test-AppHealth)) {
    return $true
  }
  Write-RestartLog "trying native-control.ps1 start"
  & $nativeControl start
  $code = $LASTEXITCODE
  Write-RestartLog ("native-control start exit=" + $code)
  return ($code -eq 0 -and (Test-AppHealth))
}

Write-RestartLog ("helper begin targetPid=" + $targetPid + " exe=" + $exe)
if (-not (Wait-ProcessGone -ProcessId $targetPid -TimeoutMs 45000)) {
  Write-RestartLog "old process still alive after wait/kill; abort"
  exit 1
}
Write-RestartLog "old process exited"
Wait-LogHandlesReleased

$ok = $false
for ($attempt = 1; $attempt -le 5; $attempt++) {
  Write-RestartLog ("launch attempt " + $attempt)
  if (Test-AppHealth) {
    Write-RestartLog "already healthy"
    $ok = $true
    break
  }
  try {
    if (Start-WithNativeControl) {
      $ok = $true
      break
    }
  } catch {
    Write-RestartLog ("native-control error: " + $_.Exception.Message)
  }

  try {
    Start-DirectProcess -WithRedirect $true | Out-Null
    Start-Sleep -Seconds 2
    if (Test-AppHealth) { $ok = $true; break }
  } catch {
    Write-RestartLog ("direct redirect start failed: " + $_.Exception.Message)
  }

  try {
    Start-DirectProcess -WithRedirect $false | Out-Null
    Start-Sleep -Seconds 2
    if (Test-AppHealth) { $ok = $true; break }
  } catch {
    Write-RestartLog ("direct no-redirect start failed: " + $_.Exception.Message)
  }

  Start-Sleep -Seconds 2
}

if ($ok -or (Test-AppHealth)) {
  Write-RestartLog "restart succeeded"
  exit 0
}
Write-RestartLog "restart failed after retries"
exit 1
`)

	return body.String()
}

func psQuote(value string) string {
	// PowerShell single-quoted string; escape embedded single quotes by doubling.
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

//go:build !windows

package sysutil

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// platformRestart spawns a detached /bin/sh helper that waits for the current
// process to exit, then relaunches the (possibly freshly updated) binary and
// health-checks it. If the new binary fails to become healthy, the helper
// restores exe + ".backup" (left by replaceExecutableAtomically) and retries,
// so a broken update rolls back automatically instead of leaving the service
// down. This works for manual nohup deployments and does not require systemd.
func platformRestart() error {
	if runtime.GOOS != "linux" {
		log.Printf("Service restart via helper only supports Linux (current OS: %s)", runtime.GOOS)
		return fmt.Errorf("service restart not supported on %s", runtime.GOOS)
	}

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

	scriptPath, err := writeLinuxRestartScript(os.Getpid(), exe, workDir)
	if err != nil {
		return err
	}

	// Detach: setsid so the helper survives this process's exit, no inherited stdio.
	cmd := exec.Command("setsid", "/bin/sh", scriptPath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		_ = os.Remove(scriptPath)
		return fmt.Errorf("start linux restart helper: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		log.Printf("linux restart helper release warning: %v", err)
	}

	log.Println("Initiating Linux service restart via detached helper...")
	log.Printf("Restart helper script: %s", scriptPath)
	log.Printf("Restart helper will relaunch: %s", exe)

	go func() {
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}

// BuildLinuxRestartScript renders the self-contained /bin/sh restart helper
// for the given process. Exported so tests and integrations can render the
// exact script the updater will run. See writeLinuxRestartScript for behavior.
func BuildLinuxRestartScript(pid int, exe, workDir string) (string, error) {
	return renderLinuxRestartScript(pid, exe, workDir)
}

// writeLinuxRestartScript writes the rendered helper to os.TempDir() and
// returns its path.
func writeLinuxRestartScript(pid int, exe, workDir string) (string, error) {
	script, err := renderLinuxRestartScript(pid, exe, workDir)
	if err != nil {
		return "", err
	}
	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("sub2api-restart-%d.sh", pid))
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		return "", fmt.Errorf("write linux restart helper script: %w", err)
	}
	return scriptPath, nil
}

// renderLinuxRestartScript builds the self-contained /bin/sh helper text. The
// script:
//  1. waits for the old PID to exit (up to 45s, then force-kill)
//  2. health-checks 127.0.0.1:18080/health
//  3. launches the new binary via nohup (hidden), logging to logs/update-restart.log
//  4. on repeated health-check failure, restores exe.backup and retries once
func renderLinuxRestartScript(pid int, exe, workDir string) (string, error) {
	runtimeEnv := filepath.Join(workDir, "runtime-env.json")
	logDir := filepath.Join(workDir, "logs")
	restartLog := filepath.Join(logDir, "update-restart.log")
	stdoutLog := filepath.Join(logDir, "sub2api.stdout.log")
	stderrLog := filepath.Join(logDir, "sub2api.stderr.log")
	pidFile := filepath.Join(workDir, "run", "sub2api.pid")
	exeBackup := exe + ".backup"

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# sub2api linux restart helper (generated)\n")
	fmt.Fprintf(&b, "TARGET_PID=%d\n", pid)
	fmt.Fprintf(&b, "EXE=%s\n", shQuote(exe))
	fmt.Fprintf(&b, "WORK_DIR=%s\n", shQuote(workDir))
	fmt.Fprintf(&b, "RUNTIME_ENV=%s\n", shQuote(runtimeEnv))
	fmt.Fprintf(&b, "LOG_DIR=%s\n", shQuote(logDir))
	fmt.Fprintf(&b, "RESTART_LOG=%s\n", shQuote(restartLog))
	fmt.Fprintf(&b, "STDOUT_LOG=%s\n", shQuote(stdoutLog))
	fmt.Fprintf(&b, "STDERR_LOG=%s\n", shQuote(stderrLog))
	fmt.Fprintf(&b, "PID_FILE=%s\n", shQuote(pidFile))
	fmt.Fprintf(&b, "EXE_BACKUP=%s\n", shQuote(exeBackup))
	b.WriteString(`
log_msg() {
  msg="[$(date '+%Y-%m-%d %H:%M:%S')] $1"
  if [ -n "$LOG_DIR" ]; then mkdir -p "$LOG_DIR" 2>/dev/null; fi
  echo "$msg" >> "$RESTART_LOG" 2>/dev/null || true
}

is_healthy() {
  if command -v curl >/dev/null 2>&1; then
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 http://127.0.0.1:18080/health 2>/dev/null)
    [ "$code" = "200" ] && return 0
  elif command -v wget >/dev/null 2>&1; then
    code=$(wget -q -O /dev/null --timeout=3 http://127.0.0.1:18080/health 2>/dev/null && echo 200 || echo 000)
    [ "$code" = "200" ] && return 0
  fi
  return 1
}

wait_pid_gone() {
  i=0
  while [ $i -lt 150 ] && kill -0 "$TARGET_PID" 2>/dev/null; do
    i=$((i+1))
    sleep 0.2
  done
  if kill -0 "$TARGET_PID" 2>/dev/null; then
    log_msg "old process still alive; force-killing $TARGET_PID"
    kill -9 "$TARGET_PID" 2>/dev/null || true
    sleep 0.5
  fi
  return 0
}

start_direct() {
  pid_dir=$(dirname "$PID_FILE")
  if [ -n "$pid_dir" ] && [ "$pid_dir" != "." ]; then mkdir -p "$pid_dir" 2>/dev/null; fi
  if [ -n "$WORK_DIR" ] && [ -d "$WORK_DIR" ]; then cd "$WORK_DIR" || true; fi
  if [ -n "$LOG_DIR" ]; then mkdir -p "$LOG_DIR" 2>/dev/null; fi
  nohup "$EXE" >> "$STDOUT_LOG" 2>> "$STDERR_LOG" &
  new_pid=$!
  echo "$new_pid" > "$PID_FILE" 2>/dev/null || true
  log_msg "started pid=$new_pid"
  return 0
}

# Wait for the old process to exit before touching health: it still serves the
# port and would otherwise make us think everything is fine ("already healthy")
# and exit, leaving the service down once it actually terminates.
wait_pid_gone
log_msg "old process exited"

attempt=0
while [ $attempt -lt 8 ]; do
  attempt=$((attempt+1))
  if is_healthy; then log_msg "already healthy"; exit 0; fi
  start_direct
  sleep 2
  if is_healthy; then log_msg "restart succeeded (attempt $attempt)"; exit 0; fi
  log_msg "health check failed attempt $attempt"
done

# Final attempt: restore backup (the updated binary failed to become healthy)
if [ -f "$EXE_BACKUP" ]; then
  log_msg "restoring backup $EXE_BACKUP -> $EXE"
  # ensure old process not holding image
  wait_pid_gone
  mv -f "$EXE_BACKUP" "$EXE" 2>/dev/null || { log_msg "backup restore failed"; exit 1; }
  chmod +x "$EXE" 2>/dev/null || true
  log_msg "backup restored; relaunching old version"
fi

attempt=0
while [ $attempt -lt 8 ]; do
  attempt=$((attempt+1))
  start_direct
  sleep 2
  if is_healthy; then log_msg "rollback restart succeeded (attempt $attempt)"; exit 0; fi
  log_msg "rollback health check failed attempt $attempt"
done

log_msg "restart and rollback both failed"
exit 1
`)

	return b.String(), nil
}

// shQuote single-quotes a string for /bin/sh, escaping embedded single quotes.
func shQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
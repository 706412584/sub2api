package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/pkg/clashsub"
)

// MihomoEngine manages per-subscription mihomo child processes.
//
// MVP: one process + config file per source id. Config hash change triggers restart.
type MihomoEngine struct {
	mu      sync.Mutex
	binary  string
	dataDir string
	procs   map[int64]*mihomoProc
}

type mihomoProc struct {
	runner     *clashsub.Runner
	configHash string
	lastError  string
	running    bool
}

// NewMihomoEngine constructs an engine. binary may be empty (resolved later via PATH).
func NewMihomoEngine(binary, dataDir string) *MihomoEngine {
	if dataDir == "" {
		dataDir = filepath.Join("data", "proxy-subscriptions")
	}
	return &MihomoEngine{
		binary:  binary,
		dataDir: dataDir,
		procs:   make(map[int64]*mihomoProc),
	}
}

// ResolveBinary returns the configured binary or looks up "mihomo" / "clash-meta" on PATH.
func (e *MihomoEngine) ResolveBinary() (string, bool) {
	if e == nil {
		return "", false
	}
	if e.binary != "" {
		if st, err := os.Stat(e.binary); err == nil && !st.IsDir() {
			return e.binary, true
		}
		if path, err := exec.LookPath(e.binary); err == nil {
			return path, true
		}
		return e.binary, false
	}
	for _, name := range []string{"mihomo", "clash-meta"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, true
		}
	}
	return "mihomo", false
}

// DataDir returns the root data directory for mihomo configs/workdirs.
func (e *MihomoEngine) DataDir() string {
	if e == nil {
		return ""
	}
	return e.dataDir
}

// EnsureRunning writes config and starts/restarts the process when hash changes.
func (e *MihomoEngine) EnsureRunning(sourceID int64, namePrefix, bindAddr string, bindings []clashsub.Binding, configHash string) error {
	if e == nil {
		return fmt.Errorf("mihomo engine not configured")
	}
	bin, ok := e.ResolveBinary()
	if !ok {
		return fmt.Errorf("mihomo binary not found (set PROXY_SUBSCRIPTION_MIHOMO_BINARY or install mihomo on PATH)")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	cfgPath := e.configPath(sourceID)
	workDir := e.workDir(sourceID)
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return err
	}

	// Config already written by caller usually; re-write is fine and keeps hash consistent.
	hash := configHash
	if hash == "" {
		h, err := clashsub.WriteMihomoConfig(cfgPath, bindAddr, namePrefix, bindings)
		if err != nil {
			return err
		}
		hash = h
	}

	proc := e.procs[sourceID]
	if proc != nil && proc.running && proc.configHash == hash && proc.runner != nil {
		return nil
	}
	if proc != nil && proc.runner != nil {
		_ = proc.runner.Stop()
	}
	runner := &clashsub.Runner{
		Binary:     bin,
		ConfigPath: cfgPath,
		DataDir:    workDir,
	}
	if err := runner.Start(); err != nil {
		e.procs[sourceID] = &mihomoProc{runner: nil, configHash: hash, lastError: err.Error(), running: false}
		return fmt.Errorf("start mihomo: %w", err)
	}
	e.procs[sourceID] = &mihomoProc{runner: runner, configHash: hash, lastError: "", running: true}
	return nil
}

// StopSource stops one source process.
func (e *MihomoEngine) StopSource(sourceID int64) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if proc, ok := e.procs[sourceID]; ok && proc != nil && proc.runner != nil {
		_ = proc.runner.Stop()
		proc.running = false
		proc.runner = nil
	}
}

// StopAll stops every managed process (server cleanup).
func (e *MihomoEngine) StopAll() {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, proc := range e.procs {
		if proc != nil && proc.runner != nil {
			_ = proc.runner.Stop()
		}
		delete(e.procs, id)
	}
}

// StatusSnapshot returns per-source runtime info.
func (e *MihomoEngine) StatusSnapshot() map[int64]ProxySubscriptionEngineSourceStatus {
	out := make(map[int64]ProxySubscriptionEngineSourceStatus)
	if e == nil {
		return out
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, proc := range e.procs {
		if proc == nil {
			continue
		}
		out[id] = ProxySubscriptionEngineSourceStatus{
			ID:         id,
			Running:    proc.running,
			ConfigHash: proc.configHash,
			LastError:  proc.lastError,
		}
	}
	return out
}

// RunningCount returns number of processes marked running.
func (e *MihomoEngine) RunningCount() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, proc := range e.procs {
		if proc != nil && proc.running {
			n++
		}
	}
	return n
}

// ConfigPathFor returns the config path used for a source.
func (e *MihomoEngine) ConfigPathFor(sourceID int64) string {
	return e.configPath(sourceID)
}

func (e *MihomoEngine) configPath(sourceID int64) string {
	return filepath.Join(e.dataDir, fmt.Sprintf("source-%d", sourceID), "config.yaml")
}

func (e *MihomoEngine) workDir(sourceID int64) string {
	return filepath.Join(e.dataDir, fmt.Sprintf("source-%d", sourceID), "workdir")
}

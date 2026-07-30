package service

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	proxySubscriptionLeaderLockKey = "proxy:subscription:sync:leader"
	proxySubscriptionLeaderLockTTL = 2 * time.Minute
	proxySubscriptionRunnerTick    = 30 * time.Second
	proxySubscriptionDueLimit      = 10
)

// ProxySubscriptionRunner periodically syncs due embedded subscription sources.
// Only the leader instance runs sync + mihomo (multi-instance safe).
type ProxySubscriptionRunner struct {
	svc        *ProxySubscriptionService
	interval   time.Duration
	stopCh     chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string
}

// NewProxySubscriptionRunner creates a runner (not started).
func NewProxySubscriptionRunner(svc *ProxySubscriptionService, interval time.Duration) *ProxySubscriptionRunner {
	if interval <= 0 {
		interval = proxySubscriptionRunnerTick
	}
	return &ProxySubscriptionRunner{
		svc:        svc,
		interval:   interval,
		stopCh:     make(chan struct{}),
		instanceID: uuid.NewString(),
	}
}

// SetLeaderLock injects distributed leader election. Nil disables gating.
func (r *ProxySubscriptionRunner) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if r == nil {
		return
	}
	r.lockCache = lockCache
	r.db = db
}

// Start begins the background loop.
func (r *ProxySubscriptionRunner) Start() {
	if r == nil || r.svc == nil || r.interval <= 0 {
		return
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		r.runOnce()
		for {
			select {
			case <-ticker.C:
				r.runOnce()
			case <-r.stopCh:
				return
			}
		}
	}()
}

// Stop ends the loop and stops mihomo engines owned by the service.
func (r *ProxySubscriptionRunner) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	r.wg.Wait()
	if r.svc != nil && r.svc.engine != nil {
		r.svc.engine.StopAll()
	}
}

func (r *ProxySubscriptionRunner) runOnce() {
	if r == nil || r.svc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	release, ok := tryAcquireSingletonLeaderLock(ctx, r.lockCache, r.db, proxySubscriptionLeaderLockKey, r.instanceID, proxySubscriptionLeaderLockTTL)
	if !ok {
		return
	}
	defer release()

	if err := r.svc.SyncDue(ctx, time.Now(), proxySubscriptionDueLimit); err != nil {
		log.Printf("[ProxySubscriptionRunner] sync due failed: %v", err)
	}
}

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
	dynamicProxyPoolLeaderLockKey = "proxy:dynamic:pool:refresh:leader"
	dynamicProxyPoolLeaderLockTTL = 2 * time.Minute
	dynamicProxyPoolRunnerTick    = 30 * time.Second
)

// DynamicProxyPoolRunner periodically refreshes enabled dynamic proxy pools.
// Only the leader instance extracts IPs (multi-instance safe).
type DynamicProxyPoolRunner struct {
	svc        *DynamicProxyPoolService
	interval   time.Duration
	stopCh     chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string
}

// NewDynamicProxyPoolRunner creates a runner (not started).
func NewDynamicProxyPoolRunner(svc *DynamicProxyPoolService, interval time.Duration) *DynamicProxyPoolRunner {
	if interval <= 0 {
		interval = dynamicProxyPoolRunnerTick
	}
	return &DynamicProxyPoolRunner{
		svc:        svc,
		interval:   interval,
		stopCh:     make(chan struct{}),
		instanceID: uuid.NewString(),
	}
}

// SetLeaderLock injects distributed leader election. Nil disables gating.
func (r *DynamicProxyPoolRunner) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if r == nil {
		return
	}
	r.lockCache = lockCache
	r.db = db
}

// Start begins the background loop.
func (r *DynamicProxyPoolRunner) Start() {
	if r == nil || r.svc == nil || r.interval <= 0 {
		return
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
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

// Stop ends the loop.
func (r *DynamicProxyPoolRunner) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	r.wg.Wait()
}

func (r *DynamicProxyPoolRunner) runOnce() {
	if r == nil || r.svc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	release, ok := tryAcquireSingletonLeaderLock(ctx, r.lockCache, r.db, dynamicProxyPoolLeaderLockKey, r.instanceID, dynamicProxyPoolLeaderLockTTL)
	if !ok {
		return
	}
	defer release()

	if err := r.svc.RefreshDue(ctx, time.Now()); err != nil {
		log.Printf("[DynamicProxyPoolRunner] refresh due failed: %v", err)
	}
}

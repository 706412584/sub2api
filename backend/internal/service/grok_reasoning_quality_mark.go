package service

import (
	"context"
	"sync"
	"time"
)

const (
	// Default TTL for Grok reasoning quality marks written by the admin probe.
	GrokReasoningQualityMarkTTL = 24 * time.Hour
	// Soft score penalty applied in LB selection for non-visible reasoning marks.
	// Large enough to lose to healthy peers, small enough not to hard-exclude.
	grokReasoningQualitySoftPenalty = 3.0
)

// GrokReasoningQualityMark records the latest probe outcome for a Grok account
// (optionally on a specific proxy egress). Keyed by account_id because visible
// reasoning quality varies primarily by account credential, not only by proxy.
type GrokReasoningQualityMark struct {
	AccountID int64                    `json:"account_id"`
	ProxyID   int64                    `json:"proxy_id,omitempty"`
	Status    GrokReasoningProbeStatus `json:"status"`
	ProbedAt  int64                    `json:"probed_at"`
	ExpiresAt int64                    `json:"expires_at"`
}

// GrokReasoningQualityMarkStore persists account-level Grok reasoning quality marks.
type GrokReasoningQualityMarkStore interface {
	Set(ctx context.Context, mark *GrokReasoningQualityMark, ttl time.Duration) error
	Get(ctx context.Context, accountID int64) (*GrokReasoningQualityMark, error)
	Delete(ctx context.Context, accountID int64) error
}

// MemoryGrokReasoningQualityMarkStore is an in-process store for tests and single-node fallback.
type MemoryGrokReasoningQualityMarkStore struct {
	mu    sync.RWMutex
	marks map[int64]GrokReasoningQualityMark
}

func NewMemoryGrokReasoningQualityMarkStore() *MemoryGrokReasoningQualityMarkStore {
	return &MemoryGrokReasoningQualityMarkStore{marks: make(map[int64]GrokReasoningQualityMark)}
}

func (s *MemoryGrokReasoningQualityMarkStore) Set(_ context.Context, mark *GrokReasoningQualityMark, ttl time.Duration) error {
	if s == nil || mark == nil || mark.AccountID <= 0 {
		return nil
	}
	if ttl <= 0 {
		ttl = GrokReasoningQualityMarkTTL
	}
	cp := *mark
	if cp.ProbedAt == 0 {
		cp.ProbedAt = time.Now().Unix()
	}
	cp.ExpiresAt = time.Now().Add(ttl).Unix()
	s.mu.Lock()
	if s.marks == nil {
		s.marks = make(map[int64]GrokReasoningQualityMark)
	}
	s.marks[cp.AccountID] = cp
	s.mu.Unlock()
	return nil
}

func (s *MemoryGrokReasoningQualityMarkStore) Get(_ context.Context, accountID int64) (*GrokReasoningQualityMark, error) {
	if s == nil || accountID <= 0 {
		return nil, nil
	}
	s.mu.RLock()
	mark, ok := s.marks[accountID]
	s.mu.RUnlock()
	if !ok {
		return nil, nil
	}
	if mark.ExpiresAt > 0 && time.Now().Unix() >= mark.ExpiresAt {
		s.mu.Lock()
		delete(s.marks, accountID)
		s.mu.Unlock()
		return nil, nil
	}
	cp := mark
	return &cp, nil
}

func (s *MemoryGrokReasoningQualityMarkStore) Delete(_ context.Context, accountID int64) error {
	if s == nil || accountID <= 0 {
		return nil
	}
	s.mu.Lock()
	delete(s.marks, accountID)
	s.mu.Unlock()
	return nil
}

func grokReasoningQualitySoftScorePenalty(status GrokReasoningProbeStatus) float64 {
	switch status {
	case GrokReasoningProbeStatusEncryptedOnly, GrokReasoningProbeStatusNoReasoning:
		return grokReasoningQualitySoftPenalty
	default:
		return 0
	}
}

// ResolveGrokReasoningQualitySoftPenalty returns the LB soft penalty for a Grok account
// based on its latest durable reasoning-quality probe mark.
func ResolveGrokReasoningQualitySoftPenalty(ctx context.Context, store GrokReasoningQualityMarkStore, account *Account) float64 {
	if store == nil || account == nil || !account.IsGrok() || account.ID <= 0 {
		return 0
	}
	mark, err := store.Get(ctx, account.ID)
	if err != nil || mark == nil {
		return 0
	}
	return grokReasoningQualitySoftScorePenalty(mark.Status)
}

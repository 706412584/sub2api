package service

import (
	"context"
	"strings"
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

// Grok reasoning visibility scheduling modes.
const (
	// GrokReasoningVisibilityModeInherit makes a group follow the gateway-level default.
	GrokReasoningVisibilityModeInherit = "inherit"
	// GrokReasoningVisibilityModeOff keeps the legacy behaviour: soft penalty only.
	GrokReasoningVisibilityModeOff = "off"
	// GrokReasoningVisibilityModeSoft deprioritizes accounts without visible reasoning.
	GrokReasoningVisibilityModeSoft = "soft"
	// GrokReasoningVisibilityModeEnforce excludes accounts without visible reasoning
	// from scheduling and quarantines them until the mark expires.
	GrokReasoningVisibilityModeEnforce = "enforce"
)

// NormalizeGrokReasoningVisibilityMode validates a stored group mode value.
// Unknown values fall back to inherit so a bad row never changes scheduling.
func NormalizeGrokReasoningVisibilityMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case GrokReasoningVisibilityModeOff:
		return GrokReasoningVisibilityModeOff
	case GrokReasoningVisibilityModeSoft:
		return GrokReasoningVisibilityModeSoft
	case GrokReasoningVisibilityModeEnforce:
		return GrokReasoningVisibilityModeEnforce
	default:
		return GrokReasoningVisibilityModeInherit
	}
}

// ResolveGrokReasoningVisibilityMode resolves the effective mode for a request,
// where an inherit (or empty) group mode falls back to the gateway default.
func ResolveGrokReasoningVisibilityMode(groupMode, gatewayMode string) string {
	resolved := NormalizeGrokReasoningVisibilityMode(groupMode)
	if resolved != GrokReasoningVisibilityModeInherit {
		return resolved
	}
	gateway := NormalizeGrokReasoningVisibilityMode(gatewayMode)
	if gateway == GrokReasoningVisibilityModeInherit {
		return GrokReasoningVisibilityModeOff
	}
	return gateway
}

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

// resolveGrokReasoningVisibleAffinity returns a positive LB boost for Grok
// accounts with a known visible-reasoning mark, so they are preferred over
// unmarked peers in the load-balance selection order.
func resolveGrokReasoningVisibleAffinity(ctx context.Context, store GrokReasoningQualityMarkStore, account *Account) float64 {
	if store == nil || account == nil || !account.IsGrok() || account.ID <= 0 {
		return 0
	}
	mark, err := store.Get(ctx, account.ID)
	if err != nil || mark == nil {
		return 0
	}
	if mark.Status == GrokReasoningProbeStatusVisible {
		return grokReasoningVisibleAffinityWeight
	}
	return 0
}

// GrokReasoningMarkIsNonVisible reports whether a probe outcome means the account
// did not return plaintext (visible) reasoning.
func GrokReasoningMarkIsNonVisible(status GrokReasoningProbeStatus) bool {
	switch status {
	case GrokReasoningProbeStatusEncryptedOnly, GrokReasoningProbeStatusNoReasoning:
		return true
	default:
		return false
	}
}

// GrokReasoningVisibilityDecision is the scheduling outcome for one Grok account
// under the effective visibility mode.
type GrokReasoningVisibilityDecision struct {
	// Excluded means the account must not be scheduled for this request.
	Excluded bool
	// QuarantineUntil is the mark expiry, usable as a temp-unschedulable deadline.
	QuarantineUntil time.Time
	// Status is the probe status backing the decision (empty when unprobed).
	Status GrokReasoningProbeStatus
}

// ResolveGrokReasoningVisibilityDecision decides whether an account is schedulable
// under the effective mode. Unprobed, expired, and visible marks always pass
// (fail-open) so a missing probe never silently drains a group.
func ResolveGrokReasoningVisibilityDecision(
	ctx context.Context,
	store GrokReasoningQualityMarkStore,
	account *Account,
	mode string,
) GrokReasoningVisibilityDecision {
	if store == nil || account == nil || !account.IsGrok() || account.ID <= 0 {
		return GrokReasoningVisibilityDecision{}
	}
	if NormalizeGrokReasoningVisibilityMode(mode) != GrokReasoningVisibilityModeEnforce {
		return GrokReasoningVisibilityDecision{}
	}
	mark, err := store.Get(ctx, account.ID)
	if err != nil || mark == nil {
		return GrokReasoningVisibilityDecision{}
	}
	if !GrokReasoningMarkIsNonVisible(mark.Status) {
		return GrokReasoningVisibilityDecision{Status: mark.Status}
	}
	decision := GrokReasoningVisibilityDecision{Excluded: true, Status: mark.Status}
	if mark.ExpiresAt > 0 {
		decision.QuarantineUntil = time.Unix(mark.ExpiresAt, 0)
	}
	return decision
}

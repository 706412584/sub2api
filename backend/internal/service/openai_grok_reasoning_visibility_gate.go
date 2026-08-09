package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

const (
	// grokReasoningQuarantineDuration is the cooldown applied to an account
	// whose probe confirms non-visible reasoning under enforce mode.
	grokReasoningQuarantineDuration = 2 * time.Minute
)

// GrokReasoningVisibilityGateConfig bundles the effective enforce mode and the
// probe reuse TTL for a request. migrate the group or gateway setting.
type GrokReasoningVisibilityGateConfig struct {
	// Mode is the effective scheduling mode (off/soft/enforce).
	Mode string
	// ProbeTTLSec is the probe-result reuse duration in seconds. 0 means every
	// selection re-probes the account (no caching).
	ProbeTTLSec int
}

// resolveGrokReasoningVisibilityConfig returns the effective enforce mode and
// probe-reuse TTL for the request. Group mode/TTL win unless inherit, in which
// case the gateway default applies. Missing settings resolve to off so
// scheduling stays unchanged.
func (s *OpenAIGatewayService) resolveGrokReasoningVisibilityConfig(ctx context.Context, groupID *int64) GrokReasoningVisibilityGateConfig {
	cfg := GrokReasoningVisibilityGateConfig{Mode: GrokReasoningVisibilityModeOff, ProbeTTLSec: 0}
	if s == nil || s.settingService == nil {
		return cfg
	}
	gateway := s.settingService.GetGrokReasoningVisibilityRuntime(ctx)
	cfg.Mode = gateway.Mode
	cfg.ProbeTTLSec = gateway.ProbeTTLSec
	if groupID != nil {
		if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && group != nil && group.ID == *groupID {
			if group.GrokReasoningVisibilityMode != "" {
				cfg.Mode = group.GrokReasoningVisibilityMode
			}
			// Group-level probe_ttl_sec overrides gateway when non-negative.
			if group.GrokReasoningProbeTTLSec >= 0 {
				cfg.ProbeTTLSec = group.GrokReasoningProbeTTLSec
			}
		}
	}
	cfg.Mode = ResolveGrokReasoningVisibilityMode(cfg.Mode, gateway.Mode)
	if cfg.ProbeTTLSec < 0 {
		cfg.ProbeTTLSec = 0
	}
	return cfg
}

// resolveGrokReasoningVisibilityMode returns the effective mode for the request.
// Kept for callers that only need the mode string.
func (s *OpenAIGatewayService) resolveGrokReasoningVisibilityMode(ctx context.Context, groupID *int64) string {
	return s.resolveGrokReasoningVisibilityConfig(ctx, groupID).Mode
}

// resolveAccountProbeProxyID picks the best proxy to use for probing an
// account's Grok reasoning visibility. It prefers the account's bound proxy
// first, then falls back to the group default proxy, then the account's
// fallback-origin proxy, and finally returns 0 (unavailable).
func (s *OpenAIGatewayService) resolveAccountProbeProxyID(account *Account) int64 {
	if account == nil {
		return 0
	}
	if account.ProxyID != nil && *account.ProxyID > 0 {
		return *account.ProxyID
	}
	if account.Proxy != nil && account.Proxy.ID > 0 {
		return account.Proxy.ID
	}
	return 0
}

// rejectGrokAccountByReasoning checks whether the selected Grok account should
// be rejected by the enforce visibility gate. When no mark exists, it runs a
// synchronous real-time probe (up to 30s) and persists the result. Returns
// true + reason when the account must be excluded.
func (s *OpenAIGatewayService) rejectGrokAccountByReasoning(ctx context.Context, account *Account, groupID *int64) (bool, string) {
	if s == nil || account == nil || !account.IsGrok() || account.ID <= 0 {
		return false, ""
	}
	cfg := s.resolveGrokReasoningVisibilityConfig(ctx, groupID)
	if NormalizeGrokReasoningVisibilityMode(cfg.Mode) != GrokReasoningVisibilityModeEnforce {
		return false, ""
	}
	if s.grokReasoningQualityMarks == nil {
		return false, ""
	}
	// Reuse a cached mark only when a probe-reuse TTL is configured (non-zero).
	// When ProbeTTLSec == 0, every selection re-probes for the freshest verdict.
	if cfg.ProbeTTLSec > 0 {
		mark, err := s.grokReasoningQualityMarks.Get(ctx, account.ID)
		if err == nil && mark != nil {
			if GrokReasoningMarkIsNonVisible(mark.Status) {
				slog.Info("grok_reasoning_mark_rejected",
					"account_id", account.ID, "status", mark.Status)
				return true, string(mark.Status)
			}
			return false, "" // visible/valid mark → accept
		}
	}
	// No usable mark → run synchronous real-time probe.
	if s.grokReasoningProbeSvc == nil {
		return false, "" // no probe service → fail-open
	}
	proxyID := s.resolveAccountProbeProxyID(account)
	if proxyID <= 0 {
		slog.Warn("grok_reasoning_probe_no_proxy", "account_id", account.ID)
		return true, "no_proxy"
	}
	slog.Info("grok_reasoning_probe_sync_start", "account_id", account.ID, "proxy_id", proxyID)
	result := s.grokReasoningProbeSvc.ProbeForPool(ctx, proxyID, account.ID)
	if result == nil {
		slog.Warn("grok_reasoning_probe_nil_result", "account_id", account.ID, "proxy_id", proxyID)
		return true, "probe_failed"
	}
	slog.Info("grok_reasoning_probe_sync_complete", "account_id", account.ID, "status", result.Status, "visible", result.HasVisibleReasoning)
	if result.Status == GrokReasoningProbeStatusVisible {
		return false, ""
	}
	slog.Info("grok_reasoning_probe_rejected",
		"account_id", account.ID, "status", result.Status,
		"proxy_id", proxyID, "latency_ms", result.LatencyMs)
	return true, string(result.Status)
}

// applyGrokReasoningVisibilityQuarantine records a bounded temp-unschedulable
// When mode is enforce, the quarantine duration is fixed at 10 minutes
// regardless of the mark's natural expiry.
func (s *OpenAIGatewayService) applyGrokReasoningVisibilityQuarantine(
	ctx context.Context,
	accountID int64,
	decision GrokReasoningVisibilityDecision,
	mode string,
) {
	if s == nil || s.accountRepo == nil || accountID <= 0 {
		return
	}
	until := decision.QuarantineUntil
	if NormalizeGrokReasoningVisibilityMode(mode) == GrokReasoningVisibilityModeEnforce {
		// Enforce mode: always quarantine for a fixed 10-minute window so the
		// account gets re-evaluated frequently instead of being locked out for
		// the full mark TTL (24h).
		until = time.Now().Add(grokReasoningQuarantineDuration)
	}
	if until.IsZero() || !until.After(time.Now()) {
		return
	}
	reason := "grok reasoning not visible (" + string(decision.Status) + ")"
	bgCtx := context.WithoutCancel(ctx)
	if err := s.accountRepo.SetTempUnschedulable(bgCtx, accountID, until, reason); err != nil {
		slog.Warn("grok_reasoning_visibility_quarantine_failed", "account_id", accountID, "error", err)
	}
}

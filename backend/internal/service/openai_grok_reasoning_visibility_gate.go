package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// GrokReasoningVisibilityGateConfig bundles the effective enforce mode, probe
// reuse TTL, and quarantine cooldown for a request.
type GrokReasoningVisibilityGateConfig struct {
	// Mode is the effective scheduling mode (off/soft/enforce).
	Mode string
	// ProbeTTLSec is the probe-result reuse duration in seconds. 0 means every
	// selection re-probes the account (no caching).
	ProbeTTLSec int
	// QuarantineSec is the cooldown applied when enforce rejects an account.
	// 0 = skip temp-unsched (exclude this round only);
	// N>0 = SetTempUnschedulable for N seconds;
	// -2 = SetSchedulable(false) permanently pause (group-level only).
	QuarantineSec int
}

// resolveGrokReasoningVisibilityConfig returns the effective enforce mode,
// probe-reuse TTL, and quarantine cooldown for the request. Group values win
// unless inherit/-1, in which case the gateway default applies. Missing
// settings resolve to off so scheduling stays unchanged.
func (s *OpenAIGatewayService) resolveGrokReasoningVisibilityConfig(ctx context.Context, groupID *int64) GrokReasoningVisibilityGateConfig {
	cfg := GrokReasoningVisibilityGateConfig{
		Mode:          GrokReasoningVisibilityModeOff,
		ProbeTTLSec:   0,
		QuarantineSec: GrokReasoningVisibilityQuarantineDefaultSec,
	}
	if s == nil || s.settingService == nil {
		return cfg
	}
	gateway := s.settingService.GetGrokReasoningVisibilityRuntime(ctx)
	cfg.Mode = gateway.Mode
	cfg.ProbeTTLSec = gateway.ProbeTTLSec
	cfg.QuarantineSec = gateway.QuarantineSec
	if groupID != nil {
		if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && group != nil && group.ID == *groupID {
			if group.GrokReasoningVisibilityMode != "" {
				cfg.Mode = group.GrokReasoningVisibilityMode
			}
			// Group-level probe_ttl_sec overrides gateway when non-negative.
			if group.GrokReasoningProbeTTLSec >= 0 {
				cfg.ProbeTTLSec = group.GrokReasoningProbeTTLSec
			}
			// Group quarantine: -1 inherits; -2 / 0 / N override.
			if group.GrokReasoningQuarantineSec != -1 {
				cfg.QuarantineSec = group.GrokReasoningQuarantineSec
			}
		}
	}
	cfg.Mode = ResolveGrokReasoningVisibilityMode(cfg.Mode, gateway.Mode)
	if cfg.ProbeTTLSec < 0 {
		cfg.ProbeTTLSec = 0
	}
	// Gateway path never yields -2; clamp stray negatives (except pause) to default.
	if cfg.QuarantineSec < 0 && cfg.QuarantineSec != GrokReasoningQuarantinePauseSchedulable {
		cfg.QuarantineSec = GrokReasoningVisibilityQuarantineDefaultSec
	}
	if cfg.QuarantineSec > GrokReasoningVisibilityQuarantineMaxSec {
		cfg.QuarantineSec = GrokReasoningVisibilityQuarantineMaxSec
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
	// Console / Web 会话账号不走 Build Responses 的 reasoning 可见性语义，
	// probe（Build OAuth 路径）对它们必然失败，直接放行。
	if account.Type == AccountTypeGrokConsole || account.Type == AccountTypeGrokWeb {
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

// grokReasoningVerdictKnown reports whether the rejection status is a durable
// "definitely no visible reasoning" verdict (probe stream completed and
// classified), as opposed to transport/probe errors where visibility is unknown.
func grokReasoningVerdictKnown(status GrokReasoningProbeStatus) bool {
	return status == GrokReasoningProbeStatusEncryptedOnly ||
		status == GrokReasoningProbeStatusNoReasoning
}

// applyGrokReasoningVisibilityQuarantine applies the configured quarantine after
// an enforce rejection. Semantics of QuarantineSec:
//
//	-2 → SetSchedulable(false) (pause scheduling permanently until manual resume)
//	 0 → no-op (exclude this selection only)
//	 N → SetTempUnschedulable for N seconds
//
// Smart split by rejection cause: the -2 permanent pause only applies when the
// probe reached a durable "no visible reasoning" verdict (encrypted_only /
// no_reasoning). Transport/probe errors (network, proxy, upstream 4xx/5xx) mean
// visibility is UNKNOWN — those accounts are never permanently paused; they get
// at most a short temp-unschedulable cooldown (QuarantineSec when configured as
// N seconds, else the 120s default) so transient faults self-heal.
//
// Soft mode may still honor decision.QuarantineUntil when set by the mark path.
func (s *OpenAIGatewayService) applyGrokReasoningVisibilityQuarantine(
	ctx context.Context,
	accountID int64,
	decision GrokReasoningVisibilityDecision,
	mode string,
	groupID *int64,
) {
	if s == nil || s.accountRepo == nil || accountID <= 0 {
		return
	}
	cfg := s.resolveGrokReasoningVisibilityConfig(ctx, groupID)
	// Prefer explicit mode argument when provided (callers may hardcode enforce).
	effectiveMode := mode
	if effectiveMode == "" {
		effectiveMode = cfg.Mode
	}
	reason := "grok reasoning not visible (" + string(decision.Status) + ")"
	bgCtx := context.WithoutCancel(ctx)

	if NormalizeGrokReasoningVisibilityMode(effectiveMode) == GrokReasoningVisibilityModeEnforce {
		// Unknown-visibility failures (error / no_proxy / probe_failed): never
		// permanently pause. Degrade -2 and 0 to a short temp-unschedulable
		// cooldown; keep configured N-second cooldowns as-is.
		effectiveSec := cfg.QuarantineSec
		if !grokReasoningVerdictKnown(decision.Status) {
			if effectiveSec == GrokReasoningQuarantinePauseSchedulable || effectiveSec <= 0 {
				effectiveSec = GrokReasoningVisibilityQuarantineDefaultSec
			}
		}
		switch {
		case effectiveSec == GrokReasoningQuarantinePauseSchedulable:
			if err := s.accountRepo.SetSchedulable(bgCtx, accountID, false); err != nil {
				slog.Warn("grok_reasoning_visibility_pause_failed", "account_id", accountID, "error", err)
			} else {
				slog.Info("grok_reasoning_visibility_paused",
					"account_id", accountID, "status", decision.Status, "reason", reason)
			}
			return
		case effectiveSec <= 0:
			// 0 = exclude this round only; do not write temp-unschedulable.
			return
		default:
			until := time.Now().Add(time.Duration(effectiveSec) * time.Second)
			if err := s.accountRepo.SetTempUnschedulable(bgCtx, accountID, until, reason); err != nil {
				slog.Warn("grok_reasoning_visibility_quarantine_failed", "account_id", accountID, "error", err)
			}
			return
		}
	}

	// Soft / other modes: honor mark-derived quarantine window if present.
	until := decision.QuarantineUntil
	if until.IsZero() || !until.After(time.Now()) {
		return
	}
	if err := s.accountRepo.SetTempUnschedulable(bgCtx, accountID, until, reason); err != nil {
		slog.Warn("grok_reasoning_visibility_quarantine_failed", "account_id", accountID, "error", err)
	}
}

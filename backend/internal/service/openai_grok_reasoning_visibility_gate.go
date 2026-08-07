package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// resolveGrokReasoningVisibilityMode returns the effective mode for the request:
// the group's mode wins unless it is inherit, in which case the gateway default
// applies. Missing settings resolve to off so scheduling stays unchanged.
func (s *OpenAIGatewayService) resolveGrokReasoningVisibilityMode(ctx context.Context, groupID *int64) string {
	if s == nil || s.settingService == nil {
		return GrokReasoningVisibilityModeOff
	}
	gateway := s.settingService.GetGrokReasoningVisibilityRuntime(ctx)
	groupMode := GrokReasoningVisibilityModeInherit
	if groupID != nil {
		if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && group != nil && group.ID == *groupID {
			groupMode = group.GrokReasoningVisibilityMode
		}
	}
	return ResolveGrokReasoningVisibilityMode(groupMode, gateway.Mode)
}

// applyGrokReasoningVisibilityQuarantine records a bounded temp-unschedulable
// window so the account carries a visible cooldown badge in the admin UI. The
// scheduling decision itself already excluded the account; this only makes the
// exclusion observable and is therefore best-effort.
func (s *OpenAIGatewayService) applyGrokReasoningVisibilityQuarantine(
	ctx context.Context,
	accountID int64,
	decision GrokReasoningVisibilityDecision,
) {
	if s == nil || s.accountRepo == nil || accountID <= 0 || decision.QuarantineUntil.IsZero() {
		return
	}
	if !decision.QuarantineUntil.After(time.Now()) {
		return
	}
	reason := "grok reasoning not visible (" + string(decision.Status) + ")"
	bgCtx := context.WithoutCancel(ctx)
	if err := s.accountRepo.SetTempUnschedulable(bgCtx, accountID, decision.QuarantineUntil, reason); err != nil {
		slog.Warn("grok_reasoning_visibility_quarantine_failed", "account_id", accountID, "error", err)
	}
}

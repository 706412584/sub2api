package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizeGrokReasoningVisibilityMode(t *testing.T) {
	require.Equal(t, GrokReasoningVisibilityModeInherit, NormalizeGrokReasoningVisibilityMode(""))
	require.Equal(t, GrokReasoningVisibilityModeInherit, NormalizeGrokReasoningVisibilityMode("bogus"))
	require.Equal(t, GrokReasoningVisibilityModeOff, NormalizeGrokReasoningVisibilityMode("off"))
	require.Equal(t, GrokReasoningVisibilityModeSoft, NormalizeGrokReasoningVisibilityMode("soft"))
	require.Equal(t, GrokReasoningVisibilityModeEnforce, NormalizeGrokReasoningVisibilityMode("enforce"))
}

func TestResolveGrokReasoningVisibilityMode(t *testing.T) {
	// Group mode wins when explicit.
	require.Equal(t, GrokReasoningVisibilityModeOff,
		ResolveGrokReasoningVisibilityMode("off", GrokReasoningVisibilityModeEnforce))
	require.Equal(t, GrokReasoningVisibilityModeEnforce,
		ResolveGrokReasoningVisibilityMode("enforce", GrokReasoningVisibilityModeOff))
	// Inherit falls back to the gateway default.
	require.Equal(t, GrokReasoningVisibilityModeEnforce,
		ResolveGrokReasoningVisibilityMode("inherit", GrokReasoningVisibilityModeEnforce))
	require.Equal(t, GrokReasoningVisibilityModeSoft,
		ResolveGrokReasoningVisibilityMode("", GrokReasoningVisibilityModeSoft))
	// Both unset resolves to off so scheduling stays unchanged.
	require.Equal(t, GrokReasoningVisibilityModeOff,
		ResolveGrokReasoningVisibilityMode("", ""))
}

func TestResolveGrokReasoningVisibilityDecision(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryGrokReasoningQualityMarkStore()
	grok := &Account{ID: 7, Platform: PlatformGrok, Type: AccountTypeOAuth}

	// Unprobed account passes even under enforce (fail-open).
	decision := ResolveGrokReasoningVisibilityDecision(ctx, store, grok, GrokReasoningVisibilityModeEnforce)
	require.False(t, decision.Excluded)

	// Visible reasoning passes.
	require.NoError(t, store.Set(ctx, &GrokReasoningQualityMark{
		AccountID: grok.ID,
		Status:    GrokReasoningProbeStatusVisible,
	}, time.Hour))
	decision = ResolveGrokReasoningVisibilityDecision(ctx, store, grok, GrokReasoningVisibilityModeEnforce)
	require.False(t, decision.Excluded)
	require.Equal(t, GrokReasoningProbeStatusVisible, decision.Status)

	// Encrypted-only is excluded under enforce, with a quarantine deadline.
	require.NoError(t, store.Set(ctx, &GrokReasoningQualityMark{
		AccountID: grok.ID,
		Status:    GrokReasoningProbeStatusEncryptedOnly,
	}, time.Hour))
	decision = ResolveGrokReasoningVisibilityDecision(ctx, store, grok, GrokReasoningVisibilityModeEnforce)
	require.True(t, decision.Excluded)
	require.Equal(t, GrokReasoningProbeStatusEncryptedOnly, decision.Status)
	require.True(t, decision.QuarantineUntil.After(time.Now()))

	// Same mark is NOT excluded under soft/off — those only affect LB score.
	require.False(t, ResolveGrokReasoningVisibilityDecision(ctx, store, grok, GrokReasoningVisibilityModeSoft).Excluded)
	require.False(t, ResolveGrokReasoningVisibilityDecision(ctx, store, grok, GrokReasoningVisibilityModeOff).Excluded)

	// no_reasoning is excluded too.
	require.NoError(t, store.Set(ctx, &GrokReasoningQualityMark{
		AccountID: grok.ID,
		Status:    GrokReasoningProbeStatusNoReasoning,
	}, time.Hour))
	require.True(t, ResolveGrokReasoningVisibilityDecision(ctx, store, grok, GrokReasoningVisibilityModeEnforce).Excluded)

	// Non-Grok accounts are never gated.
	openai := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	require.NoError(t, store.Set(ctx, &GrokReasoningQualityMark{
		AccountID: openai.ID,
		Status:    GrokReasoningProbeStatusNoReasoning,
	}, time.Hour))
	require.False(t, ResolveGrokReasoningVisibilityDecision(ctx, store, openai, GrokReasoningVisibilityModeEnforce).Excluded)

	// Nil store never gates.
	require.False(t, ResolveGrokReasoningVisibilityDecision(ctx, nil, grok, GrokReasoningVisibilityModeEnforce).Excluded)
}

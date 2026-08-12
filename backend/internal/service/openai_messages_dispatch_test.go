package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func TestNormalizeOpenAIMessagesDispatchModelConfig(t *testing.T) {
	t.Parallel()

	cfg := normalizeOpenAIMessagesDispatchModelConfig(OpenAIMessagesDispatchModelConfig{
		OpusMappedModel:   " gpt-5.4-high ",
		SonnetMappedModel: "gpt-5.3-codex",
		HaikuMappedModel:  " gpt-5.4-mini-medium ",
		ExactModelMappings: map[string]string{
			" claude-sonnet-4-5-20250929 ": " gpt-5.2-high ",
			"":                             "gpt-5.4",
			"claude-opus-4-6":              " ",
		},
	})

	require.Equal(t, "gpt-5.4", cfg.OpusMappedModel)
	require.Equal(t, "gpt-5.3-codex", cfg.SonnetMappedModel)
	require.Equal(t, "gpt-5.4-mini", cfg.HaikuMappedModel)
	require.Equal(t, map[string]string{
		"claude-sonnet-4-5-20250929": "gpt-5.2",
	}, cfg.ExactModelMappings)
}

func TestGroupResolveMessagesDispatchModel_GrokRequiresCrossClientMapping(t *testing.T) {
	original := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(original) })
	group := &Group{Platform: PlatformGrok}

	xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{})
	require.Empty(t, group.ResolveMessagesDispatchModel("claude-sonnet-4-5"))

	xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{
		DefaultText:          "grok-build-0.1",
		EnableCrossClientMap: true,
	})
	require.Equal(t, "grok-build-0.1", group.ResolveMessagesDispatchModel("claude-sonnet-4-5"))
	require.Equal(t, "grok-build-0.1", group.ResolveMessagesDispatchModel("claude-opus-4-6"))
	require.Equal(t, "grok-build-0.1", group.ResolveMessagesDispatchModel("claude-haiku-4-5"))
	require.Empty(t, group.ResolveMessagesDispatchModel("grok"))
	require.Empty(t, group.ResolveMessagesDispatchModel("gpt-5.3-codex"))
}

func TestNormalizeGrokMessagesProtocol(t *testing.T) {
	t.Parallel()

	// Default remains native responses; only explicit chat_completions opts in.
	require.Equal(t, GrokMessagesProtocolResponses, NormalizeGrokMessagesProtocol(PlatformGrok, ""))
	require.Equal(t, GrokMessagesProtocolResponses, NormalizeGrokMessagesProtocol(PlatformGrok, "invalid"))
	require.Equal(t, GrokMessagesProtocolChatCompletions, NormalizeGrokMessagesProtocol(PlatformGrok, GrokMessagesProtocolChatCompletions))
	require.Equal(t, GrokMessagesProtocolResponses, NormalizeGrokMessagesProtocol(PlatformGrok, GrokMessagesProtocolResponses))
	require.Equal(t, GrokMessagesProtocolResponses, NormalizeGrokMessagesProtocol(PlatformOpenAI, GrokMessagesProtocolChatCompletions))
}

func TestSanitizeGroupMessagesDispatchFields_ClearsNonOpenAIPlatform(t *testing.T) {
	t.Parallel()

	group := &Group{
		Platform:              PlatformAnthropic,
		AllowMessagesDispatch: true,
		DefaultMappedModel:    "gpt-5.6-sol",
		MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
			SonnetMappedModel: "gpt-5.3-codex",
			ExactModelMappings: map[string]string{
				"claude-fable-5": "gpt-5.6-sol",
			},
		},
	}

	sanitizeGroupMessagesDispatchFields(group)

	require.False(t, group.AllowMessagesDispatch)
	require.Empty(t, group.DefaultMappedModel)
	require.Equal(t, OpenAIMessagesDispatchModelConfig{}, group.MessagesDispatchModelConfig)
	require.Equal(t, GrokMessagesProtocolResponses, group.GrokMessagesProtocol)
}

func TestSanitizeGroupMessagesDispatchFields_DefaultsGrokToResponses(t *testing.T) {
	t.Parallel()

	group := &Group{Platform: PlatformGrok, GrokMessagesProtocol: "invalid"}
	sanitizeGroupMessagesDispatchFields(group)

	require.Equal(t, GrokMessagesProtocolResponses, group.GrokMessagesProtocol)

	group = &Group{Platform: PlatformGrok, GrokMessagesProtocol: GrokMessagesProtocolChatCompletions}
	sanitizeGroupMessagesDispatchFields(group)
	require.Equal(t, GrokMessagesProtocolChatCompletions, group.GrokMessagesProtocol)
}

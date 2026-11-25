package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestApplyGroupPromptPolicy(t *testing.T) {
	policy := GroupPromptPolicy{
		Enabled: true,
		Rules: []domain.GroupPromptPolicyRule{
			{
				Enabled:   true,
				Endpoints: []domain.GroupPromptPolicyEndpoint{domain.GroupPromptPolicyEndpointChatCompletions},
				Targets:   []domain.GroupPromptPolicyTarget{domain.GroupPromptPolicyTargetSystem, domain.GroupPromptPolicyTargetMessageText},
				Mode:      domain.GroupPromptPolicyModeReplace,
				Match:     domain.GroupPromptPolicyMatch{Kind: domain.GroupPromptPolicyMatchKindLiteral, Value: "OLD", CaseSensitive: true},
				Value:     "NEW",
			},
			{
				Enabled:   true,
				Endpoints: []domain.GroupPromptPolicyEndpoint{domain.GroupPromptPolicyEndpointChatCompletions},
				Targets:   []domain.GroupPromptPolicyTarget{domain.GroupPromptPolicyTargetMessageText},
				Mode:      domain.GroupPromptPolicyModeAppend,
				Match:     domain.GroupPromptPolicyMatch{Kind: domain.GroupPromptPolicyMatchKindLiteral, Value: "NEW", CaseSensitive: true},
				Value:     "!",
			},
		},
	}
	result, err := ApplyGroupPromptPolicy([]byte(`{"messages":[{"role":"system","content":"OLD system"},{"role":"user","content":[{"type":"text","text":"OLD user"},{"type":"image_url","image_url":{"url":"data:image/png"}}]}]}`), domain.GroupPromptPolicyEndpointChatCompletions, policy)
	require.NoError(t, err)
	require.True(t, result.Modified)

	var request map[string]any
	require.NoError(t, json.Unmarshal(result.Body, &request))
	messages, ok := request["messages"].([]any)
	require.True(t, ok)
	firstMessage, ok := messages[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "NEW system", firstMessage["content"])
	secondMessage, ok := messages[1].(map[string]any)
	require.True(t, ok)
	content, ok := secondMessage["content"].([]any)
	require.True(t, ok)
	firstContent, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "NEW user!", firstContent["text"])
	secondContent, ok := content[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "image_url", secondContent["type"])
}

func TestApplyGroupPromptPolicyMessagesAndResponses(t *testing.T) {
	policy := GroupPromptPolicy{Enabled: true, Rules: []domain.GroupPromptPolicyRule{{
		Enabled:   true,
		Endpoints: []domain.GroupPromptPolicyEndpoint{domain.GroupPromptPolicyEndpointMessages, domain.GroupPromptPolicyEndpointResponses},
		Targets:   []domain.GroupPromptPolicyTarget{domain.GroupPromptPolicyTargetSystem, domain.GroupPromptPolicyTargetInstructions, domain.GroupPromptPolicyTargetMessageText},
		Mode:      domain.GroupPromptPolicyModePrepend,
		Match:     domain.GroupPromptPolicyMatch{Kind: domain.GroupPromptPolicyMatchKindRegex, Value: "prompt", CaseSensitive: false},
		Value:     "prefix: ",
	}}}

	messages, err := ApplyGroupPromptPolicy([]byte(`{"system":"Prompt","messages":[{"content":[{"type":"text","text":"prompt"}]}]}`), domain.GroupPromptPolicyEndpointMessages, policy)
	require.NoError(t, err)
	require.JSONEq(t, `{"system":"prefix: Prompt","messages":[{"content":[{"type":"text","text":"prefix: prompt"}]}]}`, string(messages.Body))

	responses, err := ApplyGroupPromptPolicy([]byte(`{"instructions":"prompt","input":[{"type":"input_text","text":"prompt"}]}`), domain.GroupPromptPolicyEndpointResponses, policy)
	require.NoError(t, err)
	require.JSONEq(t, `{"instructions":"prefix: prompt","input":[{"type":"input_text","text":"prefix: prompt"}]}`, string(responses.Body))
}

func TestApplyGroupPromptPolicyBlocksWithoutMutation(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"blocked phrase"}]}`)
	policy := GroupPromptPolicy{Enabled: true, Rules: []domain.GroupPromptPolicyRule{{
		Enabled:   true,
		Endpoints: []domain.GroupPromptPolicyEndpoint{domain.GroupPromptPolicyEndpointChatCompletions},
		Targets:   []domain.GroupPromptPolicyTarget{domain.GroupPromptPolicyTargetMessageText},
		Mode:      domain.GroupPromptPolicyModeBlock,
		Match:     domain.GroupPromptPolicyMatch{Kind: domain.GroupPromptPolicyMatchKindLiteral, Value: "blocked", CaseSensitive: true},
	}}}
	result, err := ApplyGroupPromptPolicy(body, domain.GroupPromptPolicyEndpointChatCompletions, policy)
	require.NoError(t, err)
	require.True(t, result.Blocked)
	require.False(t, result.Modified)
	require.Equal(t, body, result.Body)
}

func TestApplyGroupPromptPolicyNoop(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"keep"}]}`)
	result, err := ApplyGroupPromptPolicy(body, domain.GroupPromptPolicyEndpointChatCompletions, GroupPromptPolicy{})
	require.NoError(t, err)
	require.False(t, result.Modified)
	require.False(t, result.Blocked)
	require.Equal(t, body, result.Body)
}

func TestValidateGroupPromptPolicy(t *testing.T) {
	valid := GroupPromptPolicy{Enabled: true, Rules: []domain.GroupPromptPolicyRule{{
		Endpoints: []domain.GroupPromptPolicyEndpoint{domain.GroupPromptPolicyEndpointMessages},
		Targets:   []domain.GroupPromptPolicyTarget{domain.GroupPromptPolicyTargetMessageText},
		Mode:      domain.GroupPromptPolicyModeReplace,
		Match:     domain.GroupPromptPolicyMatch{Kind: domain.GroupPromptPolicyMatchKindRegex, Value: "old"},
		Value:     "new",
	}}}
	require.NoError(t, ValidateGroupPromptPolicy(valid))

	valid.Rules[0].Match.Value = "["
	require.Error(t, ValidateGroupPromptPolicy(valid))
}

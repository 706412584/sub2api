//go:build unit

package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTransformClaudeToKiro_TextAndTools(t *testing.T) {
	req := kiroClaudeRequest{
		Model:  "claude-sonnet-4.6",
		System: json.RawMessage(`"be concise"`),
		Messages: []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}{
			{Role: "user", Content: json.RawMessage(`"hello"`)},
			{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":"hi"}]`)},
			{Role: "user", Content: json.RawMessage(`"world"`)},
		},
		Tools: []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		}{
			{Name: "lookup", Description: "look things up", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}

	out, err := transformClaudeToKiro(req, "claude-sonnet-4.6")
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4.6", out.ConversationState.CurrentMessage.UserInputMessage.ModelID)
	require.Contains(t, out.ConversationState.CurrentMessage.UserInputMessage.Content, "world")
	require.Contains(t, out.ConversationState.CurrentMessage.UserInputMessage.Content, "be concise")
	require.Len(t, out.ConversationState.History, 2)
	require.Len(t, out.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools, 1)
	require.Equal(t, "lookup", out.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools[0].ToolSpecification.Name)
}

func TestKiroGatewayServiceForwardAPIKeyNonStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stream := kiroTestEventFrame("assistantResponseEvent", []byte(`{"content":"ok"}`))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(stream)),
	}}}
	svc := NewKiroGatewayService(upstream, nil, nil, nil)
	account := &Account{
		ID:       7,
		Platform: PlatformKiro,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"kiro_api_key": "ksk_test-value",
			"auth_method":  "api_key",
			"endpoint":     "cli",
			"api_region":   "us-west-2",
		},
	}
	body := []byte(`{"model":"claude-sonnet-4.6","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, err := svc.Forward(c.Request.Context(), c, account, body, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "claude-sonnet-4.6", result.Model)
	require.False(t, result.Stream)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "q.us-west-2.amazonaws.com", upstream.requests[0].URL.Host)
	require.Equal(t, "Bearer ksk_test-value", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "API_KEY", upstream.requests[0].Header.Get("tokentype"))
	require.NotContains(t, w.Body.String(), "ksk_test-value")
	require.Contains(t, w.Body.String(), `"ok"`)
	require.Contains(t, w.Body.String(), `"type":"message"`)
}

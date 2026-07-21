//go:build unit

package service

import (
	"bytes"
	"context"
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

func TestKiroGatewayServiceForwardAsChatCompletionsNonStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stream := kiroTestEventFrame("assistantResponseEvent", []byte(`{"content":"chat-ok"}`))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(stream)),
	}}}
	svc := NewKiroGatewayService(upstream, nil, nil, nil)
	account := &Account{
		ID:       8,
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
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	result, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "claude-sonnet-4.6", result.Model)
	require.False(t, result.Stream)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "Bearer ksk_test-value", upstream.requests[0].Header.Get("Authorization"))
	require.NotContains(t, w.Body.String(), "ksk_test-value")
	require.Contains(t, w.Body.String(), "chat-ok")
	require.Contains(t, w.Body.String(), `"object":"chat.completion"`)
}

func TestKiroGatewayServiceForwardAsResponsesNonStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stream := kiroTestEventFrame("assistantResponseEvent", []byte(`{"content":"resp-ok"}`))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(stream)),
	}}}
	svc := NewKiroGatewayService(upstream, nil, nil, nil)
	account := &Account{
		ID:       9,
		Platform: PlatformKiro,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"kiro_api_key": "ksk_test-value",
			"auth_method":  "api_key",
			"endpoint":     "cli",
			"api_region":   "us-west-2",
		},
	}
	body := []byte(`{"model":"claude-sonnet-4.6","stream":false,"input":"hello"}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	result, err := svc.ForwardAsResponses(c.Request.Context(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "claude-sonnet-4.6", result.Model)
	require.False(t, result.Stream)
	require.Len(t, upstream.requests, 1)
	require.NotContains(t, w.Body.String(), "ksk_test-value")
	require.Contains(t, w.Body.String(), "resp-ok")
	require.Contains(t, w.Body.String(), `"object":"response"`)
}

func TestTransformClaudeToKiro_ImageAndServerToolFilter(t *testing.T) {
	pngB64 := "iVBORw0KGgo=" // minimal valid-looking base64
	req := kiroClaudeRequest{
		Model: "claude-sonnet-4.6",
		Messages: []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}{
			{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"describe"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + pngB64 + `"}}]`)},
		},
		Tools: []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		}{
			{Name: "lookup", Description: "client tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "web_search_20250305", Description: "server tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}
	out, err := transformClaudeToKiro(req, "claude-sonnet-4.6")
	require.NoError(t, err)
	require.Len(t, out.ConversationState.CurrentMessage.UserInputMessage.Images, 1)
	require.Equal(t, "png", out.ConversationState.CurrentMessage.UserInputMessage.Images[0].Format)
	require.NotEmpty(t, out.ConversationState.CurrentMessage.UserInputMessage.Images[0].Source.Bytes)
	require.Len(t, out.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools, 1)
	require.Equal(t, "lookup", out.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools[0].ToolSpecification.Name)
}

func TestTransformClaudeToKiro_RejectsImageURL(t *testing.T) {
	req := kiroClaudeRequest{
		Model: "claude-sonnet-4.6",
		Messages: []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}{
			{Role: "user", Content: json.RawMessage(`[{"type":"image","source":{"type":"url","url":"https://example.com/a.png"}}]`)},
		},
	}
	_, err := transformClaudeToKiro(req, "claude-sonnet-4.6")
	require.Error(t, err)
	require.Contains(t, err.Error(), "base64")
}

func TestKiroGatewayServiceForwardAsChatCompletionsStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stream := kiroTestEventFrame("assistantResponseEvent", []byte(`{"content":"stream-ok"}`))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(stream)),
	}}}
	svc := NewKiroGatewayService(upstream, nil, nil, nil)
	account := &Account{
		ID:       10,
		Platform: PlatformKiro,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"kiro_api_key": "ksk_test-value",
			"auth_method":  "api_key",
			"endpoint":     "cli",
			"api_region":   "us-west-2",
		},
	}
	body := []byte(`{"model":"claude-sonnet-4.6","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	result, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")
	require.Contains(t, w.Body.String(), "stream-ok")
	require.Contains(t, w.Body.String(), "data: [DONE]")
	require.NotContains(t, w.Body.String(), "ksk_test-value")
}

func TestKiroGatewayServiceForwardAsResponsesStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stream := kiroTestEventFrame("assistantResponseEvent", []byte(`{"content":"resp-stream"}`))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(stream)),
	}}}
	svc := NewKiroGatewayService(upstream, nil, nil, nil)
	account := &Account{
		ID:       11,
		Platform: PlatformKiro,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"kiro_api_key": "ksk_test-value",
			"auth_method":  "api_key",
			"endpoint":     "cli",
			"api_region":   "us-west-2",
		},
	}
	body := []byte(`{"model":"claude-sonnet-4.6","stream":true,"input":"hello"}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	result, err := svc.ForwardAsResponses(c.Request.Context(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")
	require.Contains(t, w.Body.String(), "response.completed")
	require.Contains(t, w.Body.String(), "resp-stream")
	require.NotContains(t, w.Body.String(), "ksk_test-value")
}

func TestKiroGatewayServiceFetchAvailableModels(t *testing.T) {
	payload := []byte(`{"models":[{"modelId":"claude-sonnet-4.6","modelName":"Sonnet"}]}`)
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(payload)),
	}}}
	svc := NewKiroGatewayService(upstream, nil, nil, nil)
	account := &Account{
		ID:       12,
		Platform: PlatformKiro,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"kiro_api_key": "ksk_test-value",
			"auth_method":  "api_key",
			"endpoint":     "cli",
			"api_region":   "us-east-1",
		},
	}
	models, err := svc.FetchAvailableModels(context.Background(), account)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "claude-sonnet-4.6", models[0].ModelID)
	require.Len(t, upstream.requests, 1)
	require.Contains(t, upstream.requests[0].URL.Path, "ListAvailableModels")
	require.Equal(t, "Bearer ksk_test-value", upstream.requests[0].Header.Get("Authorization"))
}

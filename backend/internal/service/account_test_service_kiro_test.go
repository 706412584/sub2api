package service

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccountTestService_TestAccountConnection_KiroUsesNativeEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	account := Account{
		ID:          42,
		Name:        "kiro-oauth",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"region":       "us-east-1",
			"machine_id":   "abcdef123456",
		},
	}
	repo := stubOpenAIAccountRepo{accounts: []Account{account}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader([]byte("eventstream"))),
	}}
	svc := &AccountTestService{
		accountRepo:  repo,
		httpUpstream: upstream,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/42/test", bytes.NewReader(nil))

	err := svc.TestAccountConnection(c, account.ID, "claude-sonnet-4-5-20250929", "", AccountTestModeDefault)
	require.NoError(t, err)

	require.Equal(t, "https://q.us-east-1.amazonaws.com/generateAssistantResponse", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer kiro-access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Content-Type"))
	require.Equal(t, "vibe", upstream.lastReq.Header.Get("x-amzn-kiro-agent-mode"))
	require.Equal(t, "true", upstream.lastReq.Header.Get("x-amzn-codewhisperer-optout"))
	require.NotEmpty(t, upstream.lastReq.Header.Get("Amz-Sdk-Invocation-Id"))
	require.Contains(t, upstream.lastReq.Header.Get("User-Agent"), "KiroIDE-")
	require.Contains(t, upstream.lastReq.Header.Get("User-Agent"), "abcdef123456")
	require.Empty(t, upstream.lastReq.Header.Get("anthropic-version"))
	require.Equal(t, "claude-sonnet-4.6", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.modelId").String())
	require.Equal(t, "AI_EDITOR", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.origin").String())
	require.Contains(t, rec.Body.String(), `"type":"test_complete"`)
}

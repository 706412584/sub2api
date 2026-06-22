package service

import (
	"bytes"
	"context"
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
	require.Equal(t, "claude-sonnet-4.5", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.modelId").String())
	require.Equal(t, "AI_EDITOR", gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.origin").String())
	require.Equal(t, "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK", gjson.GetBytes(upstream.lastBody, "profileArn").String())
	require.Contains(t, rec.Body.String(), `"type":"test_complete"`)
}

func TestAccountTestService_TestAccountConnection_KiroRefreshesOnAuthFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	account := Account{
		ID:           43,
		Name:         "kiro-oauth",
		Platform:     PlatformKiro,
		Type:         AccountTypeOAuth,
		Status:       StatusError,
		ErrorMessage: "Kiro API returned 401: Invalid bearer token",
		Schedulable:  true,
		Concurrency:  1,
		Credentials: map[string]any{
			"access_token":  "expired-token",
			"refresh_token": "refresh-token",
			"region":        "us-east-1",
		},
	}
	repo := &kiroTestUpdateRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{account}}}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"type":"error","error":{"type":"authentication_error","message":"Invalid bearer token"}}`))),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
			Body:       io.NopCloser(bytes.NewReader([]byte("eventstream"))),
		},
	}}
	originalRefresh := refreshKiroAccountTokenForTest
	refreshKiroAccountTokenForTest = func(_ context.Context, _ *Account) (*KiroTokenInfo, error) {
		return &KiroTokenInfo{
			AccessToken:  "fresh-token",
			RefreshToken: "new-refresh-token",
			ExpiresIn:    3600,
			ExpiresAt:    1893456000,
			TokenType:    "Bearer",
		}, nil
	}
	defer func() { refreshKiroAccountTokenForTest = originalRefresh }()

	svc := &AccountTestService{
		accountRepo:  repo,
		httpUpstream: upstream,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/43/test", bytes.NewReader(nil))

	err := svc.TestAccountConnection(c, account.ID, "claude-sonnet-4-5-20250929", "", AccountTestModeDefault)
	require.NoError(t, err)

	require.Len(t, upstream.requests, 2)
	require.Equal(t, "Bearer expired-token", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "Bearer fresh-token", upstream.requests[1].Header.Get("Authorization"))
	require.NotNil(t, repo.updated)
	require.Equal(t, "fresh-token", repo.updated.GetCredential("access_token"))
	require.Equal(t, "new-refresh-token", repo.updated.GetCredential("refresh_token"))
	require.Equal(t, 1, repo.clearErrorCalls)
	require.Contains(t, rec.Body.String(), `"type":"test_complete"`)
}

type kiroTestUpdateRepo struct {
	stubOpenAIAccountRepo
	updated         *Account
	clearErrorCalls int
}

func (r *kiroTestUpdateRepo) Update(_ context.Context, account *Account) error {
	copied := *account
	r.updated = &copied
	return nil
}

func (r *kiroTestUpdateRepo) ClearError(_ context.Context, _ int64) error {
	r.clearErrorCalls++
	return nil
}

func TestResolveKiroProfileArn(t *testing.T) {
	t.Run("explicit profile_arn wins", func(t *testing.T) {
		acct := &Account{Credentials: map[string]any{
			"profile_arn": "arn:aws:codewhisperer:eu-west-1:111:profile/custom",
			"region":      "us-east-1",
		}}
		require.Equal(t, "arn:aws:codewhisperer:eu-west-1:111:profile/custom", resolveKiroProfileArn(acct))
	})

	t.Run("falls back to public default for known region", func(t *testing.T) {
		acct := &Account{Credentials: map[string]any{"region": "eu-central-1"}}
		require.Equal(t, "arn:aws:codewhisperer:eu-central-1:699475941385:profile/EHGA3GRVQMUK", resolveKiroProfileArn(acct))
	})

	t.Run("unknown region falls back to us-east-1 default", func(t *testing.T) {
		acct := &Account{Credentials: map[string]any{"region": "ap-southeast-2"}}
		require.Equal(t, "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK", resolveKiroProfileArn(acct))
	})

	t.Run("missing region falls back to us-east-1 default", func(t *testing.T) {
		acct := &Account{Credentials: map[string]any{}}
		require.Equal(t, "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK", resolveKiroProfileArn(acct))
	})
}

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestParseGrokReasoningProbeStream_VisibleSummary(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"step "}`,
		"",
		`data: {"type":"response.reasoning_summary_text.delta","delta":"one"}`,
		"",
		`data: {"type":"response.completed","response":{"usage":{"output_tokens":12,"output_tokens_details":{"reasoning_tokens":5}},"output":[]}}`,
		"",
	}, "\n")

	parsed := parseGrokReasoningProbeStream(strings.NewReader(sse))
	require.True(t, parsed.streamCompleted)
	require.Equal(t, len([]rune("step one")), parsed.visibleReasoningChars)
	require.Equal(t, 5, parsed.reasoningTokens)
	require.Equal(t, 12, parsed.outputTokens)
	require.False(t, parsed.hasEncryptedReasoning)

	result := &GrokReasoningProbeResult{
		VisibleReasoningChars: parsed.visibleReasoningChars,
		HasVisibleReasoning:   parsed.visibleReasoningChars > 0,
		HasEncryptedReasoning: parsed.hasEncryptedReasoning,
		ReasoningTokens:       parsed.reasoningTokens,
		StreamCompleted:       parsed.streamCompleted,
	}
	status, msg := classifyGrokReasoningProbe(result)
	require.Equal(t, GrokReasoningProbeStatusVisible, status)
	require.Contains(t, msg, "visible reasoning")
}

func TestParseGrokReasoningProbeStream_EncryptedOnly(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_item.done","item":{"type":"reasoning","encrypted_content":"enc-blob","summary":[]}}`,
		"",
		`data: {"type":"response.completed","response":{"usage":{"output_tokens":20,"output_tokens_details":{"reasoning_tokens":8}},"output":[{"type":"reasoning","encrypted_content":"enc-blob","summary":[]}]}}`,
		"",
	}, "\n")

	parsed := parseGrokReasoningProbeStream(strings.NewReader(sse))
	require.True(t, parsed.streamCompleted)
	require.Equal(t, 0, parsed.visibleReasoningChars)
	require.True(t, parsed.hasEncryptedReasoning)
	require.Equal(t, 8, parsed.reasoningTokens)

	result := &GrokReasoningProbeResult{
		HasEncryptedReasoning: parsed.hasEncryptedReasoning,
		ReasoningTokens:       parsed.reasoningTokens,
		StreamCompleted:       parsed.streamCompleted,
	}
	status, msg := classifyGrokReasoningProbe(result)
	require.Equal(t, GrokReasoningProbeStatusEncryptedOnly, status)
	require.Contains(t, msg, "encrypted")
}

func TestParseGrokReasoningProbeStream_VisibleThenFailedIsError(t *testing.T) {
	sse := strings.Join([]string{
		`event: response.reasoning_summary_text.delta`,
		`data: {"delta":"partial"}`,
		``,
		`event: response.failed`,
		`data: {"response":{"status":"failed"}}`,
		``,
	}, "\n")

	parsed := parseGrokReasoningProbeStream(strings.NewReader(sse))
	require.False(t, parsed.streamCompleted)
	require.True(t, parsed.streamFailed)
	require.Equal(t, len([]rune("partial")), parsed.visibleReasoningChars)

	status, _ := classifyGrokReasoningProbe(&GrokReasoningProbeResult{
		HasVisibleReasoning:   true,
		VisibleReasoningChars: parsed.visibleReasoningChars,
		StreamCompleted:       parsed.streamCompleted,
	})
	require.Equal(t, GrokReasoningProbeStatusError, status)
}

func TestParseGrokReasoningProbeStream_EncryptedThenEOFIsError(t *testing.T) {
	parsed := parseGrokReasoningProbeStream(strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.done","item":{"type":"reasoning","encrypted_content":"enc"}}`,
		``,
	}, "\n")))
	require.False(t, parsed.streamCompleted)
	require.True(t, parsed.hasEncryptedReasoning)

	status, _ := classifyGrokReasoningProbe(&GrokReasoningProbeResult{
		HasEncryptedReasoning: parsed.hasEncryptedReasoning,
		StreamCompleted:       parsed.streamCompleted,
	})
	require.Equal(t, GrokReasoningProbeStatusError, status)
}

func TestParseGrokReasoningProbeStream_NoReasoning(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"323"}`,
		"",
		`data: {"type":"response.completed","response":{"usage":{"output_tokens":1},"output":[{"type":"message"}]}}`,
		"",
	}, "\n")

	parsed := parseGrokReasoningProbeStream(strings.NewReader(sse))
	require.True(t, parsed.streamCompleted)
	require.Equal(t, 0, parsed.visibleReasoningChars)
	require.False(t, parsed.hasEncryptedReasoning)
	require.Equal(t, 0, parsed.reasoningTokens)

	result := &GrokReasoningProbeResult{StreamCompleted: true}
	status, _ := classifyGrokReasoningProbe(result)
	require.Equal(t, GrokReasoningProbeStatusNoReasoning, status)
}

func TestBuildGrokReasoningProbeBody(t *testing.T) {
	body, err := buildGrokReasoningProbeBody()
	require.NoError(t, err)
	require.Contains(t, string(body), `"stream":true`)
	require.Contains(t, string(body), `"summary":"auto"`)
	require.Contains(t, string(body), `reasoning.encrypted_content`)
	// Must not embed secrets or oversized prompts.
	require.NotContains(t, string(body), "Bearer")
	require.Less(t, len(body), 1024)
}

func TestClassifyGrokReasoningProbe_IncompleteStream(t *testing.T) {
	status, msg := classifyGrokReasoningProbe(&GrokReasoningProbeResult{StreamCompleted: false})
	require.Equal(t, GrokReasoningProbeStatusError, status)
	require.Contains(t, msg, "response.completed")
}

func TestSummarizeGrokReasoningProbeHTTPError_NoRawLeak(t *testing.T) {
	msg := summarizeGrokReasoningProbeHTTPError(401)
	require.Equal(t, "upstream HTTP 401", msg)
	require.NotContains(t, msg, "secret")
}

type grokReasoningProbeAccountRepoStub struct {
	account *Account
}

func (s *grokReasoningProbeAccountRepoStub) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	if s.account == nil || len(ids) != 1 || ids[0] != s.account.ID {
		return nil, nil
	}
	return []*Account{s.account}, nil
}

type grokReasoningProbeProxyRepoStub struct {
	proxies map[int64]*Proxy
}

func (s *grokReasoningProbeProxyRepoStub) GetByID(_ context.Context, id int64) (*Proxy, error) {
	proxy := s.proxies[id]
	if proxy == nil {
		return nil, errors.New("proxy not found")
	}
	return proxy, nil
}

type grokReasoningProbeTokenStub struct {
	accountID int64
	token     string
}

func (s *grokReasoningProbeTokenStub) GetAccessTokenForManualTest(_ context.Context, account *Account) (string, error) {
	s.accountID = account.ID
	return s.token, nil
}

type grokReasoningProbeUpstreamStub struct {
	requestURL         string
	proxyURL           string
	accountID          int64
	accountConcurrency int
	authorization      string
	testHeader         string
	body               string
	response           *http.Response
	err                error
}

func (s *grokReasoningProbeUpstreamStub) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	s.requestURL = req.URL.String()
	s.proxyURL = proxyURL
	s.accountID = accountID
	s.accountConcurrency = accountConcurrency
	s.authorization = req.Header.Get("Authorization")
	s.testHeader = req.Header.Get("X-Test-Override")
	body, _ := io.ReadAll(req.Body)
	s.body = string(body)
	if s.response == nil && s.err == nil {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(strings.Join([]string{
				`data: {"type":"response.reasoning_summary_text.delta","delta":"brief"}`,
				"",
				`data: {"type":"response.completed","response":{"usage":{"output_tokens":6,"output_tokens_details":{"reasoning_tokens":4}},"output":[]}}`,
				"",
			}, "\n"))),
		}, nil
	}
	return s.response, s.err
}

func TestGrokReasoningProbeService_RequiresQuotaConfirmation(t *testing.T) {
	svc := &GrokReasoningProbeService{}
	_, err := svc.Probe(context.Background(), 5, GrokReasoningProbeRequest{AccountID: 9})
	require.Error(t, err)
	require.True(t, infraerrors.IsBadRequest(err))
	require.Contains(t, err.Error(), "confirm_quota_cost")
}

func TestGrokReasoningProbeService_RejectsNonGrokOAuthAccount(t *testing.T) {
	account := &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	svc := &GrokReasoningProbeService{
		accountRepo:       &grokReasoningProbeAccountRepoStub{account: account},
		proxyRepo:         &grokReasoningProbeProxyRepoStub{proxies: map[int64]*Proxy{5: {ID: 5}}},
		grokTokenProvider: &grokReasoningProbeTokenStub{},
		httpUpstream:      &grokReasoningProbeUpstreamStub{},
	}

	_, err := svc.Probe(context.Background(), 5, GrokReasoningProbeRequest{AccountID: 9, ConfirmQuotaCost: true})
	require.Error(t, err)
	require.True(t, infraerrors.IsBadRequest(err))
	require.Contains(t, err.Error(), "Grok OAuth")
}

func TestGrokReasoningProbeService_ForcesSelectedProxyAndReturnsMetadataOnly(t *testing.T) {
	boundProxyID := int64(99)
	account := &Account{
		ID:          9,
		Name:        "credential-account",
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		ProxyID:     &boundProxyID,
		Proxy:       &Proxy{ID: boundProxyID, Protocol: "http", Host: "bound.example", Port: 9000},
		Concurrency: 0,
		Credentials: map[string]any{
			"access_token":            "access-secret",
			"refresh_token":           "refresh-secret",
			"expires_at":              time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"base_url":                "https://custom-relay.example/v1",
			"model_mapping":           map[string]any{grokReasoningProbeModel: "mapped-model"},
			"header_override_enabled": true,
			"header_overrides":        map[string]any{"x-test-override": "contaminated"},
		},
	}
	selectedProxy := &Proxy{ID: 5, Protocol: "socks5", Host: "127.0.0.1", Port: 27890, Username: "user", Password: "pass"}
	tokenProvider := &grokReasoningProbeTokenStub{token: "access-secret"}
	upstream := &grokReasoningProbeUpstreamStub{}
	svc := &GrokReasoningProbeService{
		accountRepo:       &grokReasoningProbeAccountRepoStub{account: account},
		proxyRepo:         &grokReasoningProbeProxyRepoStub{proxies: map[int64]*Proxy{5: selectedProxy, 99: account.Proxy}},
		grokTokenProvider: tokenProvider,
		httpUpstream:      upstream,
	}

	result, err := svc.Probe(context.Background(), 5, GrokReasoningProbeRequest{
		AccountID:        account.ID,
		ConfirmQuotaCost: true,
	})
	require.NoError(t, err)
	require.Equal(t, selectedProxy.URL(), upstream.proxyURL)
	require.NotEqual(t, account.Proxy.URL(), upstream.proxyURL)
	require.Equal(t, account.ID, tokenProvider.accountID)
	require.Equal(t, account.ID, upstream.accountID)
	require.Equal(t, 1, upstream.accountConcurrency)
	require.Equal(t, "Bearer access-secret", upstream.authorization)
	require.Equal(t, "https://cli-chat-proxy.grok.com/v1/responses", upstream.requestURL)
	require.Empty(t, upstream.testHeader)
	require.Contains(t, upstream.body, `"model":"grok-4.5"`)
	require.NotContains(t, upstream.body, "mapped-model")
	require.Contains(t, upstream.body, `"summary":"auto"`)

	require.Equal(t, GrokReasoningProbeStatusVisible, result.Status)
	require.True(t, result.HasVisibleReasoning)
	require.Equal(t, len([]rune("brief")), result.VisibleReasoningChars)
	require.Equal(t, 4, result.ReasoningTokens)
	require.Equal(t, 6, result.OutputTokens)
	require.NotContains(t, result.Message, "brief")
	require.NotContains(t, result.Message, "access-secret")
	require.NotContains(t, result.Message, "refresh-secret")
}

func TestGrokReasoningProbeService_TransportErrorDoesNotLeakProxyCredentials(t *testing.T) {
	account := &Account{
		ID:       9,
		Name:     "credential-account",
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "access-secret",
			"refresh_token": "refresh-secret",
			"expires_at":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		},
	}
	upstream := &grokReasoningProbeUpstreamStub{err: errors.New("dial socks5://user:password@host failed")}
	svc := &GrokReasoningProbeService{
		accountRepo:       &grokReasoningProbeAccountRepoStub{account: account},
		proxyRepo:         &grokReasoningProbeProxyRepoStub{proxies: map[int64]*Proxy{5: {ID: 5, Protocol: "socks5", Host: "host", Port: 1080}}},
		grokTokenProvider: &grokReasoningProbeTokenStub{token: "access-secret"},
		httpUpstream:      upstream,
	}

	result, err := svc.Probe(context.Background(), 5, GrokReasoningProbeRequest{AccountID: 9, ConfirmQuotaCost: true})
	require.NoError(t, err)
	require.Equal(t, GrokReasoningProbeStatusError, result.Status)
	require.Contains(t, result.Message, "upstream request failed")
	require.Contains(t, result.Message, "dial socks5")
	require.Contains(t, result.Message, "***:***@")
	require.NotContains(t, result.Message, "password")
}

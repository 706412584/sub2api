//go:build unit

package service

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIUsageToClaudeUsageSplitsExclusiveBuckets(t *testing.T) {
	// 往返一致性：claudeUsageToOpenAIUsage 合并缓存明细为总输入，
	// openAIUsageToClaudeUsage 必须无损拆回互斥桶。
	origin := ClaudeUsage{
		InputTokens:              250,
		OutputTokens:             166,
		CacheCreationInputTokens: 100,
		CacheReadInputTokens:     173056,
	}
	round := openAIUsageToClaudeUsage(claudeUsageToOpenAIUsage(&origin))
	require.Equal(t, origin.InputTokens, round.InputTokens)
	require.Equal(t, origin.OutputTokens, round.OutputTokens)
	require.Equal(t, origin.CacheCreationInputTokens, round.CacheCreationInputTokens)
	require.Equal(t, origin.CacheReadInputTokens, round.CacheReadInputTokens)
}

func TestOpenAIUsageToClaudeUsageClampsNegativeInput(t *testing.T) {
	// 某些上游把 cache_read 报得比 prompt_tokens 大（数据不一致），
	// 拆桶必须钳 0 而非产生负计费。
	usage := openAIUsageToClaudeUsage(OpenAIUsage{
		InputTokens:          100,
		CacheReadInputTokens: 300,
		OutputTokens:         5,
	})
	require.Equal(t, 0, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
}

func TestMapCodebuddyStreamErrorFrameModelUnavailable(t *testing.T) {
	svc := &CodebuddyGatewayService{}
	fo := svc.mapStreamErrorFrame([]byte(`{"code":11102,"msg":"model not available"}`))
	require.NotNil(t, fo)
	require.True(t, fo.RetryableOnSameAccount, "11102 应允许同账号重试")
	require.True(t, fo.RequestScopedTransient, "11102 与账号无关，不得据此封禁账号")
	require.Equal(t, GatewayFailureScopeRequest, fo.Scope)
	require.NotEqual(t, GatewayFailureStageAccountAuth, fo.Stage)
}

func TestIsCodebuddyStreamErrorFrame(t *testing.T) {
	require.True(t, isCodebuddyStreamErrorFrame(`{"code":11102,"msg":"x"}`))
	require.True(t, isCodebuddyStreamErrorFrame(`{"error":{"message":"boom"}}`))
	// 正常 chunk（带 choices）即使携带 error 之外的键也不算错误帧
	require.False(t, isCodebuddyStreamErrorFrame(`{"id":"1","choices":[{"index":0,"delta":{"content":"hi"}}]}`))
	// usage-only 帧（无 choices 无 error）不是错误帧
	require.False(t, isCodebuddyStreamErrorFrame(`{"id":"1","usage":{"prompt_tokens":5}}`))
}

func codebuddyTestAccount() *Account {
	return &Account{
		ID:       7,
		Platform: PlatformCodebuddy,
		Type:     codebuddy.AccountTypeCN,
		Credentials: map[string]any{
			"access_token":  "cb-test-token",
			"uid":           "u-1",
			"enterprise_id": "e-1",
			"refresh_token": "rt-secret",
		},
	}
}

func codebuddySSE(frames ...string) io.Reader {
	var buf bytes.Buffer
	for _, f := range frames {
		_, _ = buf.WriteString("data: " + f + "\n\n")
	}
	_, _ = buf.WriteString("data: [DONE]\n\n")
	return bytes.NewReader(buf.Bytes())
}

func TestCodebuddyForwardMessagesStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sse := codebuddySSE(
		`{"id":"cc-1","object":"chat.completion.chunk","created":1,"model":"glm-5.2","choices":[{"index":0,"delta":{"role":"assistant","content":"hel"},"finish_reason":null}]}`,
		`{"id":"cc-1","object":"chat.completion.chunk","created":1,"model":"glm-5.2","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}`,
		`{"id":"cc-1","object":"chat.completion.chunk","created":1,"model":"glm-5.2","choices":[],"usage":{"prompt_tokens":120,"completion_tokens":6}}`,
	)
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(sse),
	}}}
	svc := NewCodebuddyGatewayService(upstream, nil, nil, nil)
	account := codebuddyTestAccount()
	body := []byte(`{"model":"glm-5.2","stream":true,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, err := svc.Forward(c.Request.Context(), c, account, body, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "glm-5.2", result.Model)
	require.True(t, result.Stream)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "Bearer cb-test-token", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "u-1", upstream.requests[0].Header.Get("X-User-Id"))
	require.NotContains(t, w.Body.String(), "rt-secret", "refresh token 不得出现在任何出站请求")
	require.NotContains(t, upstream.requests[0].Header.Get("X-Refresh-Token"), "rt-secret")
	// 上游恒流式契约：出站请求体必须被 PrepareBody 强制 stream=true
	outBody := readRequestBody(t, upstream.requests[0])
	require.Contains(t, string(outBody), `"stream":true`)
	// usage：OpenAI 总输入 120 → Claude 非缓存输入 120（无缓存明细）
	require.Equal(t, 120, result.Usage.InputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	// SSE 输出：Anthropic 事件形状（流式按分片下发，两段 delta）
	require.Contains(t, w.Body.String(), "message_start")
	require.Contains(t, w.Body.String(), "message_stop")
	require.Contains(t, w.Body.String(), `"text":"hel"`)
	require.Contains(t, w.Body.String(), `"text":"lo"`)
}

func TestCodebuddyForwardAsChatCompletionsNonStreamAggregates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sse := codebuddySSE(
		`{"id":"cc-2","object":"chat.completion.chunk","created":2,"model":"glm-5.2","choices":[{"index":0,"delta":{"role":"assistant","content":"wor"},"finish_reason":null}]}`,
		`{"id":"cc-2","object":"chat.completion.chunk","created":2,"model":"glm-5.2","choices":[{"index":0,"delta":{"content":"ld"},"finish_reason":"stop"}]}`,
		`{"id":"cc-2","object":"chat.completion.chunk","created":2,"model":"glm-5.2","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2}}`,
	)
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(sse),
	}}}
	svc := NewCodebuddyGatewayService(upstream, nil, nil, nil)
	account := codebuddyTestAccount()
	body := []byte(`{"model":"glm-5.2","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	result, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Equal(t, "glm-5.2", result.Model)
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Contains(t, w.Body.String(), `"object":"chat.completion"`)
	require.Contains(t, w.Body.String(), "world")
	require.Contains(t, w.Header().Get("Content-Type"), "application/json")
}

func TestCodebuddyForwardAsResponsesStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sse := codebuddySSE(
		`{"id":"cc-3","object":"chat.completion.chunk","created":3,"model":"glm-5.2","choices":[{"index":0,"delta":{"role":"assistant","content":"resp"},"finish_reason":null}]}`,
		`{"id":"cc-3","object":"chat.completion.chunk","created":3,"model":"glm-5.2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"cc-3","object":"chat.completion.chunk","created":3,"model":"glm-5.2","choices":[],"usage":{"prompt_tokens":8,"completion_tokens":1}}`,
	)
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(sse),
	}}}
	svc := NewCodebuddyGatewayService(upstream, nil, nil, nil)
	account := codebuddyTestAccount()
	body := []byte(`{"model":"glm-5.2","stream":true,"input":"hi"}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	result, err := svc.ForwardAsResponses(c.Request.Context(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "glm-5.2", result.Model)
	require.Equal(t, 8, result.Usage.InputTokens)
	require.Contains(t, w.Body.String(), "response.output_text.delta")
	require.Contains(t, w.Body.String(), "response.completed")
	require.Contains(t, w.Body.String(), "resp")
}

func TestCodebuddyForwardModelUnavailableFrameFailsOver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sse := codebuddySSE(`{"code":11102,"msg":"The model is unavailable"}`)
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(sse),
	}}}
	svc := NewCodebuddyGatewayService(upstream, nil, nil, nil)
	account := codebuddyTestAccount()
	body := []byte(`{"model":"bad-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, err := svc.Forward(c.Request.Context(), c, account, body, false)
	require.Error(t, err)
	require.Nil(t, result)
	fo, ok := err.(*UpstreamFailoverError)
	require.True(t, ok)
	require.True(t, fo.RetryableOnSameAccount)
	require.True(t, fo.RequestScopedTransient)
	require.Equal(t, PlatformCodebuddy, fo.Platform)
}

func TestCodebuddyMapUpstreamErrorSessionDead(t *testing.T) {
	svc := &CodebuddyGatewayService{}
	err := svc.mapUpstreamError(http.StatusUnauthorized, []byte(`{"msg":"Offline user session not found"}`))
	fo, ok := err.(*UpstreamFailoverError)
	require.True(t, ok)
	require.Equal(t, GatewayFailureStageAccountAuth, fo.Stage)
	require.Equal(t, GatewayFailureScopeAccount, fo.Scope)
}

func readRequestBody(t *testing.T, req *http.Request) []byte {
	t.Helper()
	if req.Body == nil {
		return nil
	}
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	return body
}

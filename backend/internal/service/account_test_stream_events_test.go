//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newTestGinRecorder(ctx context.Context) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	if ctx == nil {
		ctx = context.Background()
	}
	c.Request = req.WithContext(ctx)
	return c, w
}

func TestProcessOpenAIStreamEmitsThinkingAndLatency(t *testing.T) {
	t.Parallel()
	svc := &AccountTestService{}
	started := time.Now().Add(-250 * time.Millisecond)
	ctx := withTestStartedAt(context.Background(), started)
	c, w := newTestGinRecorder(ctx)

	body := strings.NewReader(strings.Join([]string{
		`data: {"type":"response.reasoning_text.delta","delta":"think-a"}`,
		`data: {"type":"response.output_text.delta","delta":"hi"}`,
		`data: {"type":"response.completed"}`,
		"",
	}, "\n"))
	err := svc.processOpenAIStream(c, body)
	require.NoError(t, err)

	out := w.Body.String()
	require.Contains(t, out, `"type":"thinking"`)
	require.Contains(t, out, `"text":"think-a"`)
	require.Contains(t, out, `"type":"content"`)
	require.Contains(t, out, `"type":"test_complete"`)
	require.Contains(t, out, `"latency_ms"`)
	require.GreaterOrEqual(t, testLatencyMs(ctx), int64(200))
}

func TestProcessClaudeStreamEmitsThinking(t *testing.T) {
	t.Parallel()
	svc := &AccountTestService{}
	c, w := newTestGinRecorder(withTestStartedAt(context.Background(), time.Now()))

	body := strings.NewReader(strings.Join([]string{
		`data: {"type":"content_block_delta","delta":{"thinking":"plan"}}`,
		`data: {"type":"content_block_delta","delta":{"text":"ans"}}`,
		`data: {"type":"message_stop"}`,
		"",
	}, "\n"))
	err := svc.processClaudeStream(c, body)
	require.NoError(t, err)
	out := w.Body.String()
	require.Contains(t, out, `"type":"thinking"`)
	require.Contains(t, out, `"text":"plan"`)
	require.Contains(t, out, `"type":"content"`)
	require.Contains(t, out, `"text":"ans"`)
}

func TestWithTestProxyOverrideExported(t *testing.T) {
	t.Parallel()
	proxy := &Proxy{ID: 3, Host: "127.0.0.1", Port: 8080, Protocol: "http"}
	ctx := WithTestProxyOverride(context.Background(), &TestProxyOverride{Proxy: proxy})
	svc := &AccountTestService{}
	url := svc.resolveTestProxyURL(ctx, &Account{})
	require.Contains(t, url, "127.0.0.1")
}

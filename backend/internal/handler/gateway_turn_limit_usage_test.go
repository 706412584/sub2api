//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type turnLimitUsageUpstream struct {
	service.HTTPUpstream
	calls      int
	summaryBad bool
	mainFails  bool
	failover   bool
	accountIDs []int64
}

const turnLimitSummaryUsageJSON = `{"input_tokens":11,"output_tokens":5,"cache_read_input_tokens":7,"cache_creation_input_tokens":5,"cache_creation":{"ephemeral_5m_input_tokens":2,"ephemeral_1h_input_tokens":3}}`

func turnLimitRequestBody(req *http.Request) []byte {
	if req == nil || req.Body == nil {
		return nil
	}
	raw, _ := io.ReadAll(req.Body)
	return raw
}

// mainResponseFor 按实际上游路径返回对应协议的完整响应（流式请求返回 SSE）。
func turnLimitMainResponseFor(path string, stream bool) (string, string) {
	switch {
	case strings.Contains(path, "responses"):
		if stream {
			return "text/event-stream", `data: {"type":"response.output_text.delta","delta":"done"}` + "\n\n" +
				`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":7,"output_tokens":3}}}` + "\n\n"
		}
		return "application/json", `{"id":"resp_main","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":7,"output_tokens":3}}`
	case strings.Contains(path, "chat/completions"):
		if stream {
			return "text/event-stream", `data: {"choices":[{"delta":{"content":"done"}}]}` + "\n\n" +
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
				`data: {"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3}}` + "\n\n" + "data: [DONE]\n\n"
		}
		return "application/json", `{"id":"chatcmpl_main","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`
	default:
		if stream {
			return "text/event-stream", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7}}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		}
		return "application/json", `{"id":"msg_main","type":"message","role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":7,"output_tokens":3}}`
	}
}

func (u *turnLimitUsageUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.calls++
	u.accountIDs = append(u.accountIDs, accountID)
	requestBody := turnLimitRequestBody(req)
	stream := gjson.GetBytes(requestBody, "stream").Bool()
	path := ""
	if req != nil && req.URL != nil {
		path = req.URL.Path
	}
	status, contentType := http.StatusOK, "application/json"
	body := ""
	switch u.calls {
	case 1:
		status, body = http.StatusBadRequest, `{"error":{"code":"session_turn_limit_exceeded","message":"too many turns"}}`
	case 2:
		body = `{"content":[{"type":"text","text":"已完成读取，接下来验证；不推送、不部署。"}],"stop_reason":"end_turn","usage":` + turnLimitSummaryUsageJSON + `}`
		if u.summaryBad {
			body = strings.Replace(body, `"end_turn"`, `"max_tokens"`, 1)
		}
	default:
		switch {
		case u.failover && u.calls == 3:
			status, body = 520, `{"error":{"message":"synthetic upstream failure"}}`
		case u.mainFails:
			status, body = http.StatusBadRequest, `{"error":{"type":"invalid_request_error","message":"synthetic rejection"}}`
		default:
			contentType, body = turnLimitMainResponseFor(path, stream)
		}
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}, "X-Request-Id": {"same-upstream-id"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (u *turnLimitUsageUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

func turnLimitUsageBody(t *testing.T, endpoint, model string) []byte {
	t.Helper()
	messages := []map[string]string{{"role": "user", "content": "继续完成任务"}}
	for i := 0; i < 110; i++ {
		messages = append(messages, map[string]string{"role": "assistant", "content": "已读取文件"}, map[string]string{"role": "user", "content": "继续"})
	}
	body := map[string]any{"model": model, "stream": false, "max_tokens": 64}
	if endpoint == "responses" {
		body["input"] = messages
	} else {
		body["messages"] = messages
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return raw
}

type turnLimitUsageHarness struct {
	gateway *GatewayHandler
	openAI  *OpenAIGatewayHandler
	apiKey  *service.APIKey
	logs    *openAIWSUsageHandlerUsageLogRepoStub
	bills   *compatStreamUsageBillingRepo
	model   string
}

func newTurnLimitUsageHarness(t *testing.T, openAI bool, upstream *turnLimitUsageUpstream) turnLimitUsageHarness {
	t.Helper()
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: 1024 * 1024}, Default: config.DefaultConfig{RateMultiplier: 1}}
	platform, model := service.PlatformAnthropic, "claude-sonnet-4-6"
	if openAI {
		platform, model = service.PlatformOpenAI, "gpt-5.1"
	}
	groupID := int64(9300)
	price := 0.000001
	group := &service.Group{ID: groupID, Hydrated: true, Platform: platform, Status: service.StatusActive, RateMultiplier: 1,
		AllowMessagesDispatch: openAI,
		ModelPricing:          []service.ChannelModelPricing{{Platform: platform, Models: []string{model}, BillingMode: service.BillingModeToken, InputPrice: &price, OutputPrice: &price, CacheWritePrice: &price, CacheWrite1hPrice: &price, CacheReadPrice: &price}},
	}
	accounts := []service.Account{}
	for i, id := range []int64{9301, 9304} {
		account := service.Account{ID: id, Name: "summary-test", Platform: platform, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Priority: i, Concurrency: 1,
			Credentials:   map[string]any{"api_key": "test-key", "api_protocol": service.APIProtocolAnthropic, "base_url": "https://upstream.test"},
			AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
		}
		if openAI {
			account.Type = service.AccountTypeOAuth
			account.Credentials = map[string]any{"access_token": "token-test", "base_url": "https://upstream.test"}
		}
		accounts = append(accounts, account)
	}
	run := turnLimitUsageHarness{model: model, logs: &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 10)}, bills: &compatStreamUsageBillingRepo{}}
	run.apiKey = &service.APIKey{ID: 9302, UserID: 9303, GroupID: &groupID, Group: group, User: &service.User{ID: 9303, Balance: 100}}
	billingCache := service.NewBillingCacheService(newHandlerInflightCache(100), nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	billing := service.NewBillingService(cfg, nil)
	resolver := service.NewModelPricingResolver(nil, billing)
	settings := service.NewSettingService(&oauthCaptchaSettingRepo{values: map[string]string{service.SettingKeyRectifierSettings: `{}`}}, cfg)
	repo := openAIImagesFailoverAccountRepo{accounts: accounts}
	if openAI {
		gw := service.NewOpenAIGatewayService(repo, run.logs, run.bills, nil, nil, nil, nil, cfg, nil, nil, billing, nil, nil, upstream, &service.DeferredService{}, nil, nil, nil, nil, resolver, nil, nil, settings, nil)
		run.openAI = NewOpenAIGatewayHandler(gw, service.NewConcurrencyService(nil), billingCache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
		run.openAI.maxAccountSwitches = 1
	} else {
		snapshot := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: []*service.Account{&accounts[0], &accounts[1]}}, nil, nil, nil, nil)
		gw := service.NewGatewayService(repo, &fakeGroupRepo{group: group}, run.logs, run.bills, nil, nil, nil, nil, cfg, snapshot, nil, billing, nil, nil, nil, upstream, &service.DeferredService{}, nil, nil, nil, nil, settings, nil, nil, resolver, nil, nil, nil)
		run.gateway = &GatewayHandler{gatewayService: gw, billingCacheService: billingCache, cfg: cfg, maxAccountSwitches: 1,
			concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(&fakeConcurrencyCache{}), SSEPingFormatClaude, 0)}
	}
	return run
}

func (r *turnLimitUsageHarness) call(t *testing.T, endpoint string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "shared-client-id")
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(string(turnLimitUsageBody(t, endpoint, r.model)))).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), r.apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: r.apiKey.UserID, Concurrency: 0})
	if r.openAI != nil {
		switch endpoint {
		case "messages":
			r.openAI.Messages(c)
		case "responses":
			r.openAI.Responses(c)
		default:
			r.openAI.ChatCompletions(c)
		}
	} else {
		switch endpoint {
		case "messages":
			r.gateway.Messages(c)
		case "responses":
			r.gateway.Responses(c)
		default:
			r.gateway.ChatCompletions(c)
		}
	}
	return c, rec
}

func TestTurnLimitSummaryUsageHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, openAI := range []bool{false, true} {
		gatewayName := "gateway"
		if openAI {
			gatewayName = "openai"
		}
		for _, endpoint := range []string{"messages", "responses", "chat/completions"} {
			for _, mode := range []string{"success", "main_failure", "invalid_summary"} {
				t.Run(gatewayName+"/"+endpoint+"/"+mode, func(t *testing.T) {
					upstream := &turnLimitUsageUpstream{mainFails: mode == "main_failure", summaryBad: mode == "invalid_summary"}
					run := newTurnLimitUsageHarness(t, openAI, upstream)
					c, rec := run.call(t, endpoint)
					wantBills := 1
					if mode == "success" {
						require.Equal(t, http.StatusOK, rec.Code)
						wantBills = 2
					} else {
						require.GreaterOrEqual(t, rec.Code, 400)
					}
					run.bills.mu.Lock()
					defer run.bills.mu.Unlock()
					require.Len(t, run.bills.commands, wantBills)
					summary := run.bills.commands[0]
					require.True(t, strings.HasPrefix(summary.RequestID, "turn_limit_summary:"))
					require.EqualValues(t, 9301, summary.AccountID)
					require.Equal(t, 11, summary.InputTokens)
					require.Equal(t, 5, summary.OutputTokens)
					require.Equal(t, 7, summary.CacheReadTokens)
					require.Equal(t, 5, summary.CacheCreationTokens)
					require.Positive(t, summary.BalanceCost)
					require.NotEmpty(t, summary.RequestPayloadHash)
					require.Nil(t, service.TakeTurnLimitSummaryUsage(c))
					if wantBills == 2 {
						main := run.bills.commands[1]
						require.Equal(t, "client:shared-client-id", main.RequestID)
						require.Equal(t, 7, main.InputTokens)
						require.Equal(t, 3, main.OutputTokens)
					}
				})
			}
		}
	}
}

func TestTurnLimitSummaryUsageKeepsAttemptAccountAfterFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &turnLimitUsageUpstream{failover: true}
	run := newTurnLimitUsageHarness(t, true, upstream)
	_, rec := run.call(t, "responses")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []int64{9301, 9301, 9301, 9304}, upstream.accountIDs)
	run.bills.mu.Lock()
	defer run.bills.mu.Unlock()
	require.Len(t, run.bills.commands, 2)
	require.EqualValues(t, 9301, run.bills.commands[0].AccountID)
	require.EqualValues(t, 9304, run.bills.commands[1].AccountID)
}

func TestTurnLimitSummaryUsageAsyncSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	run := newTurnLimitUsageHarness(t, true, &turnLimitUsageUpstream{mainFails: true})
	pool := newUsageRecordTestPool(t)
	run.openAI.usageRecordWorkerPool = pool
	entered, release := make(chan struct{}), make(chan struct{})
	pool.Submit(func(context.Context) { close(entered); <-release })
	<-entered
	c, _ := run.call(t, "responses")
	run.openAI.submitTurnLimitSummaryUsage(c, run.apiKey, &service.Account{ID: 999}, nil, service.ChannelMappingResult{}, "wrong-model", time.Now())
	c.Keys = nil
	c.Request = httptest.NewRequest(http.MethodPost, "/reused", nil)
	run.apiKey = &service.APIKey{ID: 999}
	close(release)
	select {
	case usage := <-run.logs.created:
		require.EqualValues(t, 9301, usage.AccountID)
		require.EqualValues(t, 9302, usage.APIKeyID)
		require.NotEmpty(t, usage.Model)
		require.NotEqual(t, "wrong-model", usage.Model)
		require.True(t, strings.HasPrefix(usage.RequestID, "turn_limit_summary:"))
	case <-time.After(5 * time.Second):
		t.Fatal("摘要计费任务未完成")
	}
	pool.Stop()
	run.bills.mu.Lock()
	defer run.bills.mu.Unlock()
	require.Len(t, run.bills.commands, 1)
}

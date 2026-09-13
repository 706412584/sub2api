// Package-level: CodeBuddy 网关服务。
//
// 数据面形状：上游即 Chat Completions（/v2/chat/completions），但有两个
// CodeBuddy 特有约束：
//  1. 上游拒绝非流式请求（PrepareBody 强制 stream:true），非流式响应由
//     codebuddy.Aggregate 聚合 SSE 得到；
//  2. 三条入站端点（/v1/messages、/v1/chat/completions、/v1/responses）
//     各自经 apicompat 双向桥接，usage 以 OpenAIUsage 桶语义观测、
//     以 ClaudeUsage 桶语义入账（openAIUsageToClaudeUsage 换算）。
//
// 与 KiroGatewayService 一致：协议包只构造 *http.Request，执行统一交给
// HTTPUpstream（代理 / TLS 指纹 / 并发槽），本服务不持有网络客户端。
package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// CodebuddyGatewayService forwards Messages / Chat Completions / Responses
// clients through the native CodeBuddy data plane.
type CodebuddyGatewayService struct {
	httpUpstream         HTTPUpstream
	tlsFPProfileService  *TLSFingerprintProfileService
	settingService       *SettingService
	cfg                  *config.Config
	responseHeaderFilter *responseheaders.CompiledHeaderFilter
}

func NewCodebuddyGatewayService(
	httpUpstream HTTPUpstream,
	tlsFPProfileService *TLSFingerprintProfileService,
	settingService *SettingService,
	cfg *config.Config,
) *CodebuddyGatewayService {
	return &CodebuddyGatewayService{
		httpUpstream:         httpUpstream,
		tlsFPProfileService:  tlsFPProfileService,
		settingService:       settingService,
		cfg:                  cfg,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
	}
}

// ──────────────────────────────────────────────────────────
// Credential / region helpers
// ──────────────────────────────────────────────────────────

func (s *CodebuddyGatewayService) buildCredentials(account *Account) codebuddy.Credentials {
	return codebuddy.FromCredentialsMap(account.Credentials)
}

func (s *CodebuddyGatewayService) region(account *Account) codebuddy.Region {
	return codebuddy.RegionForAccountType(account.Type)
}

// ──────────────────────────────────────────────────────────
// Upstream request execution
// ──────────────────────────────────────────────────────────

// sendChat executes a prepared CodeBuddy chat body against the account's
// region endpoint. The returned response body is ALWAYS an SSE stream
// (PrepareBody forces stream:true upstream contract).
func (s *CodebuddyGatewayService) sendChat(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	chatBody []byte,
) (*http.Response, error) {
	creds := s.buildCredentials(account)
	region := s.region(account)
	req, err := codebuddy.BuildChatRequest(creds, region, chatBody, codebuddy.EndpointOptions{})
	if err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode:        http.StatusBadRequest,
			ResponseBody:      claudeErrorBody("invalid_request_error", "Failed to build CodeBuddy request"),
			Platform:          PlatformCodebuddy,
			Scope:             GatewayFailureScopeRequest,
			NextAccountAction: NextAccountStop,
		}
	}
	// 记录本次实际使用的协议端点，供 GetUpstreamEndpoint / 用量日志读取。
	SetActualOpenAIUpstreamEndpoint(c, "/v1/chat/completions")
	req = req.WithContext(WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileOpenAI))
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	var tlsProfile = (*tlsfingerprint.Profile)(nil)
	if s.tlsFPProfileService != nil {
		tlsProfile = s.tlsFPProfileService.ResolveTLSProfile(account)
	}
	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	if err != nil {
		if ctx.Err() != nil {
			return nil, s.writeChatError(c, http.StatusBadGateway, "api_error", "Client disconnected before upstream response")
		}
		return nil, &UpstreamFailoverError{
			StatusCode:             http.StatusBadGateway,
			ResponseBody:           claudeErrorBody("api_error", "CodeBuddy upstream request failed"),
			Platform:               PlatformCodebuddy,
			RetryableOnSameAccount: true,
		}
	}
	return resp, nil
}

// readUpstreamErrorBody 读取错误响应体（受 LogUpstreamErrorBody 配置上限约束）。
func (s *CodebuddyGatewayService) readUpstreamErrorBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	limit := int64(gatewayUpstreamErrorBodyReadLimit)
	if s != nil && s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody && s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes > int(limit) {
		limit = int64(s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
	return body
}

// mapUpstreamError maps a non-2xx CodeBuddy response onto the failover
// taxonomy using codebuddy.Classify. Non-2xx here is rare: most business
// errors arrive as 200 + SSE error frames, handled separately.
func (s *CodebuddyGatewayService) mapUpstreamError(status int, body []byte) error {
	kind := codebuddy.Classify(status, string(body))
	switch kind {
	case codebuddy.ErrHardCredit:
		return &UpstreamFailoverError{
			StatusCode:   status,
			ResponseBody: claudeErrorBody("rate_limit_error", "CodeBuddy credit exhausted"),
			Platform:     PlatformCodebuddy,
			Scope:        GatewayFailureScopeAccount,
		}
	case codebuddy.ErrSessionDead:
		return &UpstreamFailoverError{
			StatusCode:   status,
			ResponseBody: claudeErrorBody("authentication_error", "CodeBuddy session expired, re-login required"),
			Platform:     PlatformCodebuddy,
			Stage:        GatewayFailureStageAccountAuth,
			Scope:        GatewayFailureScopeAccount,
		}
	case codebuddy.ErrSoftRate:
		return &UpstreamFailoverError{
			StatusCode:             status,
			ResponseBody:           claudeErrorBody("rate_limit_error", "CodeBuddy rate limited"),
			Platform:               PlatformCodebuddy,
			RetryableOnSameAccount: true,
		}
	case codebuddy.ErrNotFound, codebuddy.ErrServer:
		return &UpstreamFailoverError{
			StatusCode:             status,
			ResponseBody:           claudeErrorBody("api_error", fmt.Sprintf("CodeBuddy upstream error: %d", status)),
			Platform:               PlatformCodebuddy,
			RetryableOnSameAccount: true,
		}
	default:
		return &UpstreamFailoverError{
			StatusCode:        status,
			ResponseBody:      claudeErrorBody("invalid_request_error", kiroFirstNonEmpty(strings.TrimSpace(string(body)), fmt.Sprintf("CodeBuddy upstream error: %d", status))),
			Platform:          PlatformCodebuddy,
			Scope:             GatewayFailureScopeRequest,
			NextAccountAction: NextAccountStop,
		}
	}
}

// mapStreamErrorFrame maps a JSON error object observed inside a 200 SSE
// stream (CodeBuddy delivers business errors as data frames, e.g. code 11102).
func (s *CodebuddyGatewayService) mapStreamErrorFrame(raw []byte) *UpstreamFailoverError {
	var ue struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	_ = json.Unmarshal(raw, &ue)
	if ue.Code == codebuddy.CodeModelUnavailable {
		// 「本请求的模型在该账号区域不可用」：请求级瞬时失败——换账号
		// （必要时跨区域）可解，但与账号凭据健康无关，不得封禁账号。
		return &UpstreamFailoverError{
			StatusCode:             http.StatusBadRequest,
			ResponseBody:           claudeErrorBody("invalid_request_error", kiroFirstNonEmpty(ue.Msg, "model unavailable in this region")),
			Platform:               PlatformCodebuddy,
			RetryableOnSameAccount: true,
			RequestScopedTransient: true,
			Scope:                  GatewayFailureScopeRequest,
		}
	}
	kind := codebuddy.Classify(http.StatusOK, ue.Msg)
	switch kind {
	case codebuddy.ErrHardCredit:
		return &UpstreamFailoverError{
			StatusCode:   http.StatusBadRequest,
			ResponseBody: claudeErrorBody("rate_limit_error", "CodeBuddy credit exhausted"),
			Platform:     PlatformCodebuddy,
			Scope:        GatewayFailureScopeAccount,
		}
	case codebuddy.ErrSessionDead:
		return &UpstreamFailoverError{
			StatusCode:   http.StatusBadRequest,
			ResponseBody: claudeErrorBody("authentication_error", "CodeBuddy session expired, re-login required"),
			Platform:     PlatformCodebuddy,
			Stage:        GatewayFailureStageAccountAuth,
			Scope:        GatewayFailureScopeAccount,
		}
	case codebuddy.ErrSoftRate:
		return &UpstreamFailoverError{
			StatusCode:             http.StatusBadRequest,
			ResponseBody:           claudeErrorBody("rate_limit_error", "CodeBuddy rate limited"),
			Platform:               PlatformCodebuddy,
			RetryableOnSameAccount: true,
		}
	default:
		return &UpstreamFailoverError{
			StatusCode:        http.StatusBadRequest,
			ResponseBody:      claudeErrorBody("api_error", kiroFirstNonEmpty(ue.Msg, "CodeBuddy upstream stream error")),
			Platform:          PlatformCodebuddy,
			Scope:             GatewayFailureScopeRequest,
			NextAccountAction: NextAccountStop,
		}
	}
}

// ──────────────────────────────────────────────────────────
// Usage conversion (OpenAIUsage → ClaudeUsage)
// ──────────────────────────────────────────────────────────

// openAIUsageToClaudeUsage is the inverse of claudeUsageToOpenAIUsage:
// OpenAIUsage.InputTokens counts TOTAL input (cache buckets included),
// ClaudeUsage.InputTokens counts UNCACHED input only, so the cache buckets
// must be subtracted back out (clamped at zero — RecordUsage re-merges them).
func openAIUsageToClaudeUsage(u OpenAIUsage) ClaudeUsage {
	input := u.InputTokens - u.CacheReadInputTokens - u.CacheCreationInputTokens
	if input < 0 {
		input = 0
	}
	return ClaudeUsage{
		InputTokens:              input,
		OutputTokens:             u.OutputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens,
		ImageOutputTokens:        u.ImageOutputTokens,
	}
}

// ──────────────────────────────────────────────────────────
// SSE stream scanning (shared across the three inbound endpoints)
// ──────────────────────────────────────────────────────────

func (s *CodebuddyGatewayService) newUpstreamSSEScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	return scanner
}

// codebuddyStreamScanState snapshots the raw upstream SSE read.
type codebuddyStreamScanState struct {
	Usage        OpenAIUsage
	FirstTokenMs *int
	SawDone      bool
	Err          error
}

// scanCodebuddyStream drives the CodeBuddy SSE read loop: extracts data
// lines, stops at [DONE], tracks latest usage + first-token latency, and
// feeds each parsed chunk to emit. An in-stream JSON error frame is routed
// through mapStreamErrorFrame by recording it on the state — the caller
// decides whether any content was already written to the client.
type codebuddyStreamScanFunc func(chunk *apicompat.ChatCompletionsChunk)

func (s *CodebuddyGatewayService) scanCodebuddyStream(
	c *gin.Context,
	resp *http.Response,
	logPrefix string,
	requestID string,
	startTime time.Time,
	emit codebuddyStreamScanFunc,
) (codebuddyStreamScanState, *UpstreamFailoverError) {
	var st codebuddyStreamScanState

	scanner := s.newUpstreamSSEScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		payload, ok := extractOpenAISSEDataLine(line)
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			st.SawDone = true
			break
		}
		if observer := upstreamResponseModelObserverFromContext(c); observer != nil {
			observer.ObserveOpenAI([]byte(payload), "")
		}
		if u := extractCCStreamUsage(payload); u != nil {
			st.Usage = *u
		}
		// CodeBuddy 以 200 + data 帧内 error 对象表达业务错误
		//（code=11102 等），先于 chunk 解析检查。
		if isCodebuddyStreamErrorFrame(payload) {
			return st, s.mapStreamErrorFrame([]byte(payload))
		}
		var chunk apicompat.ChatCompletionsChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			logger.L().Warn(logPrefix+": failed to parse chat stream chunk",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
			continue
		}
		if st.FirstTokenMs == nil && !isOpenAIChatUsageOnlyStreamChunk(payload) && chatChunkStartsResponsesOutput(&chunk) {
			ms := int(time.Since(startTime).Milliseconds())
			st.FirstTokenMs = &ms
		}
		emit(&chunk)
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn(logPrefix+": stream read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
		st.Err = err
	}
	return st, nil
}

// isCodebuddyStreamErrorFrame reports whether an SSE data payload is a
// CodeBuddy business-error envelope ({"code":11102,"msg":...} or an
// OpenAI-style {"error":{...}} object with no choices).
func isCodebuddyStreamErrorFrame(payload string) bool {
	if !strings.Contains(payload, `"code"`) && !strings.Contains(payload, `"error"`) {
		return false
	}
	var probe struct {
		Code    int             `json:"code"`
		Error   json.RawMessage `json:"error"`
		Choices json.RawMessage `json:"choices"`
	}
	if err := json.Unmarshal([]byte(payload), &probe); err != nil {
		return false
	}
	if probe.Code != 0 {
		return true
	}
	return len(probe.Error) > 0 && len(probe.Choices) == 0
}

// newStreamHeaderWriter returns an idempotent lazy SSE header-commit closure
// (mirrors OpenAIGatewayService.newStreamHeaderWriter; delayed until first
// event write so early upstream failure can still fail over).
func (s *CodebuddyGatewayService) newStreamHeaderWriter(c *gin.Context, upstream http.Header) func() {
	headersWritten := false
	return func() {
		if headersWritten {
			return
		}
		headersWritten = true
		if s.responseHeaderFilter != nil {
			responseheaders.WriteFilteredHeaders(c.Writer.Header(), upstream, s.responseHeaderFilter)
		}
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")
		c.Writer.WriteHeader(http.StatusOK)
	}
}

// ──────────────────────────────────────────────────────────
// Credit balance (billing meter)
// ──────────────────────────────────────────────────────────

// FetchCreditRemain queries the account's current spendable credit balance
// (sum of all package CycleCapacity entries, negative values clamped to 0).
// Purely a billing-plane read; failures do NOT affect account health.
func (s *CodebuddyGatewayService) FetchCreditRemain(ctx context.Context, account *Account) (int64, error) {
	creds := s.buildCredentials(account)
	if err := creds.Validate(); err != nil {
		return 0, err
	}
	region := s.region(account)
	body := map[string]any{
		"PageNumber":               1,
		"PageSize":                 100,
		"ProductCode":              "p_tcaca",
		"Status":                   []int{0, 3},
		"PackageEndTimeRangeBegin": time.Now().Format("2006-01-02 15:04:05"),
		"PackageEndTimeRangeEnd":   time.Now().Add(365 * 101 * 24 * time.Hour).Format("2006-01-02 15:04:05"),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := codebuddy.BuildUserResourceRequest(creds, region, raw, codebuddy.EndpointOptions{})
	if err != nil {
		return 0, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileOpenAI))
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	var tlsProfile = (*tlsfingerprint.Profile)(nil)
	if s.tlsFPProfileService != nil {
		tlsProfile = s.tlsFPProfileService.ResolveTLSProfile(account)
	}
	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return 0, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("codebuddy: user resource HTTP %d", resp.StatusCode)
	}
	data, err := codebuddy.ParseEnvelope(resp.StatusCode, respBody)
	if err != nil {
		return 0, err
	}
	var parsed struct {
		Response struct {
			Data struct {
				Accounts []struct {
					CapacitySize        int64 `json:"CapacitySize"`
					CapacityRemain      int64 `json:"CapacityRemain"`
					CapacityUsed        int64 `json:"CapacityUsed"`
					CycleCapacitySize   int64 `json:"CycleCapacitySize"`
					CycleCapacityRemain int64 `json:"CycleCapacityRemain"`
					CycleCapacityUsed   int64 `json:"CycleCapacityUsed"`
				} `json:"Accounts"`
			} `json:"Data"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return 0, fmt.Errorf("codebuddy: parse user resource: %w", err)
	}
	var remain int64
	for _, acct := range parsed.Response.Data.Accounts {
		var r int64
		switch {
		case acct.CycleCapacitySize > 0:
			r = acct.CycleCapacityRemain
		case acct.CycleCapacityRemain > 0 || acct.CycleCapacityUsed > 0:
			r = acct.CycleCapacityRemain
		default:
			r = acct.CapacityRemain
		}
		if r < 0 {
			r = 0
		}
		remain += r
	}
	return remain, nil
}

// ──────────────────────────────────────────────────────────
// Error writers (per inbound endpoint shape)
// ──────────────────────────────────────────────────────────

func (s *CodebuddyGatewayService) writeClaudeError(c *gin.Context, status int, errType, message string) error {
	MarkResponseCommitted(c)
	c.JSON(status, gin.H{
		"type":  "error",
		"error": gin.H{"type": errType, "message": message},
	})
	return fmt.Errorf("%s", message)
}

func (s *CodebuddyGatewayService) writeChatError(c *gin.Context, status int, errType, message string) error {
	MarkResponseCommitted(c)
	c.JSON(status, gin.H{"error": gin.H{"type": errType, "message": message}})
	return fmt.Errorf("%s", message)
}

func (s *CodebuddyGatewayService) writeResponsesError(c *gin.Context, status int, errType, message string) error {
	MarkResponseCommitted(c)
	c.JSON(status, gin.H{"error": gin.H{"type": errType, "message": message}})
	return fmt.Errorf("%s", message)
}

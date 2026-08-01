package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	grokReasoningProbeModel       = "grok-4.5"
	grokReasoningProbeInput       = "What is 17*19? Reply with only the number."
	grokReasoningProbeTimeout     = 45 * time.Second
	grokReasoningProbeMaxBodyScan = 2 * 1024 * 1024
)

type GrokReasoningProbeStatus string

const (
	GrokReasoningProbeStatusVisible       GrokReasoningProbeStatus = "visible"
	GrokReasoningProbeStatusEncryptedOnly GrokReasoningProbeStatus = "encrypted_only"
	GrokReasoningProbeStatusNoReasoning   GrokReasoningProbeStatus = "no_reasoning"
	GrokReasoningProbeStatusError         GrokReasoningProbeStatus = "error"
)

// GrokReasoningProbeRequest is the admin-initiated opt-in probe for visible reasoning.
type GrokReasoningProbeRequest struct {
	AccountID        int64 `json:"account_id"`
	ConfirmQuotaCost bool  `json:"confirm_quota_cost"`
}

// GrokReasoningProbeResult returns whitelist metadata only — never summary text,
// encrypted content, credentials, or raw upstream bodies.
type GrokReasoningProbeResult struct {
	ProxyID               int64                    `json:"proxy_id"`
	AccountID             int64                    `json:"account_id"`
	AccountName           string                   `json:"account_name,omitempty"`
	Model                 string                   `json:"model"`
	HTTPStatus            int                      `json:"http_status"`
	LatencyMs             int64                    `json:"latency_ms"`
	StreamCompleted       bool                     `json:"stream_completed"`
	HasVisibleReasoning   bool                     `json:"has_visible_reasoning"`
	VisibleReasoningChars int                      `json:"visible_reasoning_chars"`
	HasEncryptedReasoning bool                     `json:"has_encrypted_reasoning"`
	ReasoningTokens       int                      `json:"reasoning_tokens"`
	OutputTokens          int                      `json:"output_tokens,omitempty"`
	Status                GrokReasoningProbeStatus `json:"status"`
	Message               string                   `json:"message"`
	ProbedAt              int64                    `json:"probed_at"`
}

type grokReasoningProbeAccountRepository interface {
	GetByIDs(ctx context.Context, ids []int64) ([]*Account, error)
}

type grokReasoningProbeProxyRepository interface {
	GetByID(ctx context.Context, id int64) (*Proxy, error)
}

type grokReasoningProbeTokenProvider interface {
	GetAccessTokenForManualTest(ctx context.Context, account *Account) (string, error)
}

type grokReasoningProbeHTTPUpstream interface {
	Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error)
}

// GrokReasoningProbeService runs a real streaming Grok Responses probe through a
// forced proxy egress using any Grok OAuth account's credentials.
type GrokReasoningProbeService struct {
	accountRepo       grokReasoningProbeAccountRepository
	proxyRepo         grokReasoningProbeProxyRepository
	grokTokenProvider grokReasoningProbeTokenProvider
	httpUpstream      grokReasoningProbeHTTPUpstream
	markStore         GrokReasoningQualityMarkStore
}

func NewGrokReasoningProbeService(
	accountRepo AccountRepository,
	proxyRepo ProxyRepository,
	grokTokenProvider *GrokTokenProvider,
	httpUpstream HTTPUpstream,
) *GrokReasoningProbeService {
	return &GrokReasoningProbeService{
		accountRepo:       accountRepo,
		proxyRepo:         proxyRepo,
		grokTokenProvider: grokTokenProvider,
		httpUpstream:      httpUpstream,
	}
}

// SetMarkStore injects optional persistence for probe outcomes used by scheduling soft deprioritization.
func (s *GrokReasoningProbeService) SetMarkStore(store GrokReasoningQualityMarkStore) {
	if s == nil {
		return
	}
	s.markStore = store
}

// Probe uses the selected account only for OAuth credentials. The fixed model,
// official CLI endpoint, request headers, and forced proxy isolate egress as the
// only variable under test.
func (s *GrokReasoningProbeService) Probe(ctx context.Context, proxyID int64, req GrokReasoningProbeRequest) (*GrokReasoningProbeResult, error) {
	if s == nil {
		return nil, infraerrors.InternalServer("GROK_REASONING_PROBE_UNAVAILABLE", "grok reasoning probe service is not configured")
	}
	if !req.ConfirmQuotaCost {
		return nil, infraerrors.BadRequest("GROK_REASONING_PROBE_CONFIRM_REQUIRED", "confirm_quota_cost must be true; this probe consumes Grok quota")
	}
	if req.AccountID <= 0 {
		return nil, infraerrors.BadRequest("GROK_REASONING_PROBE_ACCOUNT_REQUIRED", "account_id is required")
	}
	if proxyID <= 0 {
		return nil, infraerrors.BadRequest("GROK_REASONING_PROBE_PROXY_REQUIRED", "proxy id is required")
	}
	if s.httpUpstream == nil {
		return nil, infraerrors.InternalServer("GROK_REASONING_PROBE_UPSTREAM_MISSING", "HTTP upstream not configured")
	}
	if s.grokTokenProvider == nil {
		return nil, infraerrors.InternalServer("GROK_REASONING_PROBE_TOKEN_PROVIDER_MISSING", "Grok token provider not configured")
	}
	if s.accountRepo == nil || s.proxyRepo == nil {
		return nil, infraerrors.InternalServer("GROK_REASONING_PROBE_REPO_MISSING", "required repositories not configured")
	}

	proxy, err := s.proxyRepo.GetByID(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	if proxy == nil {
		return nil, infraerrors.NotFound("PROXY_NOT_FOUND", "proxy not found")
	}

	account, err := s.loadGrokOAuthAccount(ctx, req.AccountID)
	if err != nil {
		return nil, err
	}

	token, err := s.grokTokenProvider.GetAccessTokenForManualTest(ctx, account)
	if err != nil {
		return nil, infraerrors.BadRequest("GROK_REASONING_PROBE_TOKEN_FAILED", "failed to get Grok access token")
	}

	body, err := buildGrokReasoningProbeBody()
	if err != nil {
		return nil, infraerrors.InternalServer("GROK_REASONING_PROBE_BODY", "failed to build probe request body")
	}

	probeCtx, cancel := context.WithTimeout(ctx, grokReasoningProbeTimeout)
	defer cancel()

	// A synthetic routing account deliberately excludes model maps, custom base_url,
	// and header overrides while retaining the shared official Grok request builder.
	routingAccount := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth}
	httpReq, err := buildGrokResponsesRequest(probeCtx, nil, routingAccount, body, token, "", nil)
	if err != nil {
		return nil, infraerrors.InternalServer("GROK_REASONING_PROBE_REQUEST", "failed to create probe request")
	}

	start := time.Now()
	resp, err := s.httpUpstream.Do(httpReq, proxy.URL(), account.ID, maxInt(account.Concurrency, 1))
	latencyMs := time.Since(start).Milliseconds()
	if err != nil {
		return &GrokReasoningProbeResult{
			ProxyID:     proxyID,
			AccountID:   account.ID,
			AccountName: account.Name,
			Model:       grokReasoningProbeModel,
			LatencyMs:   latencyMs,
			Status:      GrokReasoningProbeStatusError,
			Message:     "upstream request failed",
			ProbedAt:    time.Now().Unix(),
		}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	result := &GrokReasoningProbeResult{
		ProxyID:     proxyID,
		AccountID:   account.ID,
		AccountName: account.Name,
		Model:       grokReasoningProbeModel,
		HTTPStatus:  resp.StatusCode,
		LatencyMs:   latencyMs,
		ProbedAt:    time.Now().Unix(),
	}

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		result.Status = GrokReasoningProbeStatusError
		result.Message = summarizeGrokReasoningProbeHTTPError(resp.StatusCode)
		return result, nil
	}

	parsed := parseGrokReasoningProbeStream(io.LimitReader(resp.Body, grokReasoningProbeMaxBodyScan))
	result.StreamCompleted = parsed.streamCompleted && !parsed.streamFailed
	result.VisibleReasoningChars = parsed.visibleReasoningChars
	result.HasVisibleReasoning = parsed.visibleReasoningChars > 0
	result.HasEncryptedReasoning = parsed.hasEncryptedReasoning
	result.ReasoningTokens = parsed.reasoningTokens
	result.OutputTokens = parsed.outputTokens
	result.Status, result.Message = classifyGrokReasoningProbe(result)
	s.persistReasoningQualityMark(probeCtx, result)
	return result, nil
}

func (s *GrokReasoningProbeService) persistReasoningQualityMark(ctx context.Context, result *GrokReasoningProbeResult) {
	if s == nil || s.markStore == nil || result == nil || result.ProxyID <= 0 {
		return
	}
	// Only durable quality outcomes affect scheduling. Transport/HTTP errors stay ephemeral.
	switch result.Status {
	case GrokReasoningProbeStatusVisible, GrokReasoningProbeStatusEncryptedOnly, GrokReasoningProbeStatusNoReasoning:
	default:
		return
	}
	_ = s.markStore.Set(ctx, &GrokReasoningQualityMark{
		ProxyID:  result.ProxyID,
		Status:   result.Status,
		ProbedAt: result.ProbedAt,
	}, GrokReasoningQualityMarkTTL)
}

func (s *GrokReasoningProbeService) loadGrokOAuthAccount(ctx context.Context, accountID int64) (*Account, error) {
	// GetByIDs preloads the bound proxy required by manual token validation. The
	// bound proxy is never used for this probe's upstream request.
	accounts, err := s.accountRepo.GetByIDs(ctx, []int64{accountID})
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 || accounts[0] == nil {
		return nil, infraerrors.NotFound("ACCOUNT_NOT_FOUND", "account not found")
	}
	account := accounts[0]
	if account.Platform != PlatformGrok || account.Type != AccountTypeOAuth {
		return nil, infraerrors.BadRequest("GROK_REASONING_PROBE_ACCOUNT_TYPE", "account must be a Grok OAuth account")
	}
	if account.ProxyID != nil && account.Proxy == nil {
		if bound, loadErr := s.proxyRepo.GetByID(ctx, *account.ProxyID); loadErr == nil && bound != nil {
			account.Proxy = bound
		}
	}
	return account, nil
}

func buildGrokReasoningProbeBody() ([]byte, error) {
	input, err := json.Marshal(grokReasoningProbeInput)
	if err != nil {
		return nil, err
	}
	return json.Marshal(apicompat.ResponsesRequest{
		Model:   grokReasoningProbeModel,
		Input:   input,
		Stream:  true,
		Include: []string{"reasoning.encrypted_content"},
		Reasoning: &apicompat.ResponsesReasoning{
			Effort:  "low",
			Summary: "auto",
		},
	})
}

type grokReasoningProbeParse struct {
	streamCompleted       bool
	streamFailed          bool
	visibleReasoningChars int
	hasEncryptedReasoning bool
	reasoningTokens       int
	outputTokens          int
}

func parseGrokReasoningProbeStream(body io.Reader) grokReasoningProbeParse {
	var result grokReasoningProbeParse
	if body == nil {
		return result
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), grokReasoningProbeMaxBodyScan)
	var parser openAICompatSSEFrameParser
	terminal := false
	for scanner.Scan() {
		frame, ready := parser.AddLine(strings.TrimRight(scanner.Text(), "\r"))
		if ready && accumulateGrokReasoningProbeFrame(&result, frame) {
			terminal = true
			break
		}
	}
	if !terminal {
		if frame, ready := parser.Finish(); ready {
			_ = accumulateGrokReasoningProbeFrame(&result, frame)
		}
	}
	if scanner.Err() != nil && !result.streamCompleted {
		result.streamFailed = true
	}
	return result
}

func accumulateGrokReasoningProbeFrame(result *grokReasoningProbeParse, frame openAICompatSSEFrame) bool {
	if result == nil {
		return false
	}
	payload := openAICompatPayloadWithEventType(frame.Data, frame.EventType)
	if strings.TrimSpace(payload) == "" || strings.TrimSpace(payload) == "[DONE]" {
		return false
	}

	var event apicompat.ResponsesStreamEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return false
	}

	switch event.Type {
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		result.visibleReasoningChars += len([]rune(event.Delta))
	case "response.output_item.added", "response.output_item.done":
		applyGrokReasoningProbeOutput(result, event.Item, false)
	case "response.completed", "response.done":
		if event.Response != nil && event.Response.Status != "" && event.Response.Status != "completed" {
			result.streamFailed = true
			return true
		}
		result.streamCompleted = true
		applyGrokReasoningProbeUsage(result, event.Usage)
		if event.Response != nil {
			applyGrokReasoningProbeUsage(result, event.Response.Usage)
			for index := range event.Response.Output {
				applyGrokReasoningProbeOutput(result, &event.Response.Output[index], result.visibleReasoningChars == 0)
			}
		}
		return true
	case "response.failed", "response.incomplete", "error":
		result.streamFailed = true
		return true
	}
	return false
}

func applyGrokReasoningProbeUsage(result *grokReasoningProbeParse, usage *apicompat.ResponsesUsage) {
	if result == nil || usage == nil {
		return
	}
	result.outputTokens = usage.OutputTokens
	if usage.OutputTokensDetails != nil {
		result.reasoningTokens = usage.OutputTokensDetails.ReasoningTokens
	}
}

func applyGrokReasoningProbeOutput(result *grokReasoningProbeParse, output *apicompat.ResponsesOutput, includeSummary bool) {
	if result == nil || output == nil || output.Type != "reasoning" {
		return
	}
	if strings.TrimSpace(output.EncryptedContent) != "" {
		result.hasEncryptedReasoning = true
	}
	if includeSummary {
		for _, summary := range output.Summary {
			result.visibleReasoningChars += len([]rune(summary.Text))
		}
	}
}

func classifyGrokReasoningProbe(result *GrokReasoningProbeResult) (GrokReasoningProbeStatus, string) {
	if result == nil {
		return GrokReasoningProbeStatusError, "empty probe result"
	}
	if !result.StreamCompleted {
		return GrokReasoningProbeStatusError, "stream ended before successful response.completed"
	}
	if result.HasVisibleReasoning {
		return GrokReasoningProbeStatusVisible, fmt.Sprintf("visible reasoning summary present (%d chars)", result.VisibleReasoningChars)
	}
	if result.HasEncryptedReasoning && result.ReasoningTokens > 0 {
		return GrokReasoningProbeStatusEncryptedOnly, fmt.Sprintf("encrypted reasoning only; reasoning_tokens=%d", result.ReasoningTokens)
	}
	if result.HasEncryptedReasoning {
		return GrokReasoningProbeStatusEncryptedOnly, "encrypted reasoning present without visible summary"
	}
	if result.ReasoningTokens > 0 {
		return GrokReasoningProbeStatusNoReasoning, fmt.Sprintf("reasoning_tokens=%d but no visible or encrypted reasoning payload", result.ReasoningTokens)
	}
	return GrokReasoningProbeStatusNoReasoning, "completed without visible or encrypted reasoning"
}

func summarizeGrokReasoningProbeHTTPError(status int) string {
	return fmt.Sprintf("upstream HTTP %d", status)
}

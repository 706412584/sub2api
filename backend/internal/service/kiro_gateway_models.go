package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	kiroprotocol "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

const kiroRESTReadLimit = 1 << 20 // 1 MiB

// FetchAvailableModels queries Kiro ListAvailableModels for the account.
// Failures return an empty slice and error; callers should use fallback model lists.
func (s *KiroGatewayService) FetchAvailableModels(ctx context.Context, account *Account) ([]kiroprotocol.UpstreamModel, error) {
	creds := s.buildCredentials(account)
	if err := creds.Validate(); err != nil {
		return nil, err
	}
	opts := s.buildEndpointOptions(account)
	// REST model/quota endpoints only exist in us-east-1 / eu-central-1.
	var lastErr error
	for _, region := range kiroprotocol.RESTRegionCandidates(opts.Region) {
		regionOpts := opts
		regionOpts.Region = region
		regionCreds := creds
		if regionCreds.APIRegion == "" {
			regionCreds.APIRegion = region
		}
		req, err := kiroprotocol.BuildAvailableModelsRequest(regionCreds, regionOpts)
		if err != nil {
			lastErr = err
			continue
		}
		req = req.WithContext(ctx)
		body, status, err := s.doREST(ctx, account, req)
		if err != nil {
			lastErr = err
			continue
		}
		if status < 200 || status >= 300 {
			lastErr = fmt.Errorf("kiro models status %d", status)
			continue
		}
		var parsed kiroprotocol.ListAvailableModelsResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			lastErr = err
			continue
		}
		return parsed.Models, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("kiro models unavailable")
	}
	return nil, lastErr
}

// FetchUsageLimits queries Kiro getUsageLimits for the account.
func (s *KiroGatewayService) FetchUsageLimits(ctx context.Context, account *Account) (*kiroprotocol.UsageLimitsResponse, error) {
	creds := s.buildCredentials(account)
	if err := creds.Validate(); err != nil {
		return nil, err
	}
	opts := s.buildEndpointOptions(account)
	var lastErr error
	for _, region := range kiroprotocol.RESTRegionCandidates(opts.Region) {
		regionOpts := opts
		regionOpts.Region = region
		regionCreds := creds
		if regionCreds.APIRegion == "" {
			regionCreds.APIRegion = region
		}
		req, err := kiroprotocol.BuildUsageLimitsRequest(regionCreds, regionOpts)
		if err != nil {
			lastErr = err
			continue
		}
		req = req.WithContext(ctx)
		body, status, err := s.doREST(ctx, account, req)
		if err != nil {
			lastErr = err
			continue
		}
		if status < 200 || status >= 300 {
			lastErr = fmt.Errorf("kiro usage limits status %d", status)
			continue
		}
		var parsed kiroprotocol.UsageLimitsResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			lastErr = err
			continue
		}
		return &parsed, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("kiro usage limits unavailable")
	}
	return nil, lastErr
}

// SnapshotUsageToAccountExtra stores a versioned kiro_usage snapshot on account.Extra (in-memory only).
// Persistence is left to the caller (admin refresh / account service).
func SnapshotUsageToAccountExtra(extra map[string]any, limits *kiroprotocol.UsageLimitsResponse) map[string]any {
	if extra == nil {
		extra = map[string]any{}
	}
	if limits == nil {
		return extra
	}
	extra["kiro_usage"] = map[string]any{
		"version":            1,
		"fetched_at":         time.Now().UTC().Format(time.RFC3339),
		"subscription_title": limits.SubscriptionTitle(),
		"current_usage":      limits.CurrentUsage(),
		"usage_limit":        limits.UsageLimit(),
		"stale":              false,
	}
	return extra
}

func (s *KiroGatewayService) doREST(ctx context.Context, account *Account, req *http.Request) ([]byte, int, error) {
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	var tlsProfile = (*tlsfingerprint.Profile)(nil)
	if s.tlsFPProfileService != nil {
		tlsProfile = s.tlsFPProfileService.ResolveTLSProfile(account)
	}
	req = req.WithContext(ctx)
	account.ApplyHeaderOverrides(req.Header)
	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, kiroRESTReadLimit))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func NewProxyExitInfoProberWithEgress(cfg *config.Config, resolver EgressProxyResolver) service.ProxyExitInfoProber {
	insecure := false
	allowPrivate := false
	validateResolvedIP := true
	maxResponseBytes := defaultProxyProbeResponseMaxBytes
	if cfg != nil {
		insecure = cfg.Security.ProxyProbe.InsecureSkipVerify
		allowPrivate = cfg.Security.URLAllowlist.AllowPrivateHosts
		validateResolvedIP = cfg.Security.URLAllowlist.Enabled
		if cfg.Gateway.ProxyProbeResponseReadMaxBytes > 0 {
			maxResponseBytes = cfg.Gateway.ProxyProbeResponseReadMaxBytes
		}
	}
	if insecure {
		log.Printf("[ProxyProbe] Warning: insecure_skip_verify is not allowed and will cause probe failure.")
	}
	return &proxyProbeService{
		insecureSkipVerify: insecure,
		allowPrivateHosts:  allowPrivate,
		validateResolvedIP: validateResolvedIP,
		maxResponseBytes:   maxResponseBytes,
		egressResolver:     resolver,
	}
}

// NewProxyExitInfoProber keeps the no-egress constructor for tests and other
// callers that do not wire the proxy repository.
func NewProxyExitInfoProber(cfg *config.Config) service.ProxyExitInfoProber {
	return NewProxyExitInfoProberWithEgress(cfg, nil)
}

const (
	defaultProxyProbeTimeout          = 10 * time.Second
	defaultProxyProbeResponseMaxBytes = int64(1024 * 1024)
)

// probeURLs 按优先级排列的探测 URL 列表
// 某些 AI API 专用代理只允许访问特定域名，因此需要多个备选
var probeURLs = []struct {
	url    string
	parser string // "ip-api" or "ipify"
}{
	{"http://ip-api.com/json/?lang=zh-CN", "ip-api"},
	{"http://api64.ipify.org?format=json", "ipify"},
}

type proxyProbeService struct {
	insecureSkipVerify bool
	allowPrivateHosts  bool
	validateResolvedIP bool
	maxResponseBytes   int64
	egressResolver     EgressProxyResolver
}

func (s *proxyProbeService) ProbeProxy(ctx context.Context, proxyURL string) (*service.ProxyExitInfo, int64, error) {
	egressURL := ""
	if s != nil && s.egressResolver != nil {
		if normalized, _, err := proxyurl.Parse(proxyURL); err == nil {
			egressURL = s.egressResolver.ResolveEgress(ctx, normalized)
		}
	}
	client, err := newProxyProbeClient(proxyURL, egressURL, s.insecureSkipVerify, s.validateResolvedIP, s.allowPrivateHosts)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create proxy client: %w", err)
	}

	var lastErr error
	for _, probe := range probeURLs {
		exitInfo, latencyMs, err := s.probeWithURL(ctx, client, probe.url, probe.parser)
		if err == nil {
			return exitInfo, latencyMs, nil
		}
		lastErr = err
	}

	return nil, 0, fmt.Errorf("all probe URLs failed, last error: %w", lastErr)
}

func newProxyProbeClient(proxyURL, egressURL string, insecureSkipVerify, validateResolvedIP, allowPrivateHosts bool) (*http.Client, error) {
	if strings.TrimSpace(egressURL) == "" {
		return httpclient.GetClient(httpclient.Options{
			ProxyURL:           proxyURL,
			Timeout:            defaultProxyProbeTimeout,
			InsecureSkipVerify: insecureSkipVerify,
			ValidateResolvedIP: validateResolvedIP,
			AllowPrivateHosts:  allowPrivateHosts,
		})
	}
	_, target, err := proxyurl.Parse(proxyURL)
	if err != nil || target == nil {
		return nil, fmt.Errorf("parse target proxy: %w", err)
	}
	_, egress, err := proxyurl.Parse(egressURL)
	if err != nil || egress == nil {
		return nil, fmt.Errorf("parse egress proxy: %w", err)
	}
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	base, err := newEgressDialer(egress.String(), &net.Dialer{Timeout: 10 * time.Second})
	if err != nil {
		return nil, err
	}
	transport.DialContext = base.DialContext
	if err := proxyutil.ConfigureTransportProxyWithDialContextToProxy(transport, target, net.JoinHostPort(target.Hostname(), target.Port()), base.DialContext); err != nil {
		return nil, err
	}
	return &http.Client{Transport: transport, Timeout: defaultProxyProbeTimeout}, nil
}

func (s *proxyProbeService) probeWithURL(ctx context.Context, client *http.Client, url string, parser string) (*service.ProxyExitInfo, int64, error) {
	startTime := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("proxy connection failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	latencyMs := time.Since(startTime).Milliseconds()

	if resp.StatusCode != http.StatusOK {
		return nil, latencyMs, fmt.Errorf("request failed with status: %d", resp.StatusCode)
	}

	maxResponseBytes := s.maxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultProxyProbeResponseMaxBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, latencyMs, fmt.Errorf("failed to read response: %w", err)
	}
	if int64(len(body)) > maxResponseBytes {
		return nil, latencyMs, fmt.Errorf("proxy probe response exceeds limit: %d", maxResponseBytes)
	}

	switch parser {
	case "ip-api":
		return s.parseIPAPI(body, latencyMs)
	case "ipify":
		return s.parseIPify(body, latencyMs)
	default:
		return nil, latencyMs, fmt.Errorf("unknown parser: %s", parser)
	}
}

func (s *proxyProbeService) parseIPAPI(body []byte, latencyMs int64) (*service.ProxyExitInfo, int64, error) {
	var ipInfo struct {
		Status      string `json:"status"`
		Message     string `json:"message"`
		Query       string `json:"query"`
		City        string `json:"city"`
		Region      string `json:"region"`
		RegionName  string `json:"regionName"`
		Country     string `json:"country"`
		CountryCode string `json:"countryCode"`
	}

	if err := json.Unmarshal(body, &ipInfo); err != nil {
		preview := string(body)
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		return nil, latencyMs, fmt.Errorf("failed to parse response: %w (body: %s)", err, preview)
	}
	if strings.ToLower(ipInfo.Status) != "success" {
		if ipInfo.Message == "" {
			ipInfo.Message = "ip-api request failed"
		}
		return nil, latencyMs, fmt.Errorf("ip-api request failed: %s", ipInfo.Message)
	}

	region := ipInfo.RegionName
	if region == "" {
		region = ipInfo.Region
	}
	return &service.ProxyExitInfo{
		IP:          ipInfo.Query,
		City:        ipInfo.City,
		Region:      region,
		Country:     ipInfo.Country,
		CountryCode: ipInfo.CountryCode,
	}, latencyMs, nil
}

func (s *proxyProbeService) parseIPify(body []byte, latencyMs int64) (*service.ProxyExitInfo, int64, error) {
	var result struct {
		IP string `json:"ip"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, latencyMs, fmt.Errorf("failed to parse ipify response: %w", err)
	}
	if result.IP == "" {
		return nil, latencyMs, fmt.Errorf("ipify: no IP found in response")
	}
	return &service.ProxyExitInfo{
		IP: result.IP,
	}, latencyMs, nil
}

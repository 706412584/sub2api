package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	DynamicPoolSourceExtractAPI   = "extract_api"
	DynamicPoolSourceSubscription = "subscription"
	DynamicPoolNamePrefixBase     = "dpool-"
)

// DynamicProxyPoolService manages CRUD and extraction for dynamic proxy pools.
type DynamicProxyPoolService struct {
	repo       DynamicProxyPoolRepository
	proxyRepo  ProxyRepository
	httpClient *http.Client
}

// NewDynamicProxyPoolService constructs the service.
func NewDynamicProxyPoolService(
	repo DynamicProxyPoolRepository,
	proxyRepo ProxyRepository,
) *DynamicProxyPoolService {
	return &DynamicProxyPoolService{
		repo:      repo,
		proxyRepo: proxyRepo,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Create validates and persists a new dynamic proxy pool.
func (s *DynamicProxyPoolService) Create(ctx context.Context, p DynamicProxyPoolCreateParams) (*DynamicProxyPool, error) {
	m := &DynamicProxyPool{
		Name:               p.Name,
		Enabled:            true,
		SourceType:         coalesce(p.SourceType, DynamicPoolSourceExtractAPI),
		SubscriptionID:     p.SubscriptionID,
		ExtractURL:         p.ExtractURL,
		Protocol:           coalesce(p.Protocol, "http"),
		AuthMode:           coalesce(p.AuthMode, "none"),
		Username:           p.Username,
		Password:           p.Password,
		ResponseFormat:     coalesce(p.ResponseFormat, "txt"),
		LineSeparator:      coalesce(p.LineSeparator, "\r\n"),
		IPFieldPath:        p.IPFieldPath,
		PortFieldPath:      p.PortFieldPath,
		RefreshIntervalSec: max(p.RefreshIntervalSec, 60),
		IPDurationSec:      max(p.IPDurationSec, 30),
		ExtractCount:       max(p.ExtractCount, 1),
		MinAlive:           max(p.MinAlive, 1),
	}

	// Generate name prefix
	m.NamePrefix = fmt.Sprintf("dpool-%s-", sanitizePrefix(m.Name))

	if err := s.validate(ctx, m, 0); err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

// Get returns a pool by ID.
func (s *DynamicProxyPoolService) Get(ctx context.Context, id int64) (*DynamicProxyPool, error) {
	return s.repo.GetByID(ctx, id)
}

// List returns paginated pools.
func (s *DynamicProxyPoolService) List(ctx context.Context, params DynamicProxyPoolListParams) ([]*DynamicProxyPool, int64, error) {
	return s.repo.List(ctx, params)
}

// Update modifies an existing pool.
func (s *DynamicProxyPoolService) Update(ctx context.Context, id int64, p DynamicProxyPoolUpdateParams) (*DynamicProxyPool, error) {
	m, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if p.Name != nil {
		m.Name = *p.Name
	}
	if p.Enabled != nil {
		m.Enabled = *p.Enabled
	}
	if p.SourceType != nil {
		m.SourceType = *p.SourceType
	}
	if p.SubscriptionID != nil {
		m.SubscriptionID = p.SubscriptionID
	}
	if p.ExtractURL != nil {
		m.ExtractURL = *p.ExtractURL
	}
	if p.Protocol != nil {
		m.Protocol = *p.Protocol
	}
	if p.AuthMode != nil {
		m.AuthMode = *p.AuthMode
	}
	if p.Username != nil {
		m.Username = *p.Username
	}
	if p.Password != nil {
		m.Password = *p.Password
	}
	if p.ResponseFormat != nil {
		m.ResponseFormat = *p.ResponseFormat
	}
	if p.LineSeparator != nil {
		m.LineSeparator = *p.LineSeparator
	}
	if p.IPFieldPath != nil {
		m.IPFieldPath = *p.IPFieldPath
	}
	if p.PortFieldPath != nil {
		m.PortFieldPath = *p.PortFieldPath
	}
	if p.RefreshIntervalSec != nil {
		m.RefreshIntervalSec = max(*p.RefreshIntervalSec, 60)
	}
	if p.IPDurationSec != nil {
		m.IPDurationSec = max(*p.IPDurationSec, 30)
	}
	if p.ExtractCount != nil {
		m.ExtractCount = max(*p.ExtractCount, 1)
	}
	if p.MinAlive != nil {
		m.MinAlive = max(*p.MinAlive, 1)
	}

	if err := s.validate(ctx, m, id); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

// Delete removes a pool and its owned proxy records.
func (s *DynamicProxyPoolService) Delete(ctx context.Context, id int64) error {
	m, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	// Clean up owned proxies
	owned, err := s.proxyRepo.ListOwnedByPrefix(ctx, m.NamePrefix)
	if err != nil {
		return fmt.Errorf("list owned proxies: %w", err)
	}
	for _, p := range owned {
		if p.AccountCount == 0 {
			_ = s.proxyRepo.Delete(ctx, p.ID)
		}
	}
	return s.repo.Delete(ctx, id)
}

// Extract triggers an immediate IP extraction for the pool.
func (s *DynamicProxyPoolService) Extract(ctx context.Context, id int64) (*DynamicProxyPoolExtractResult, error) {
	m, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if m.SourceType != DynamicPoolSourceExtractAPI {
		return nil, errors.BadRequest("POOL_SOURCE_NOT_EXTRACT", "only extract_api pools support manual extraction")
	}
	if m.ExtractURL == "" {
		return nil, errors.BadRequest("POOL_NO_URL", "extract_url is empty")
	}
	return s.doExtract(ctx, m)
}

// RefreshDue extracts new IPs for enabled pools whose alive count dropped below
// min_alive or whose refresh interval has elapsed. Expired proxy rows owned by a
// pool are pruned first so alive counts reflect reality.
func (s *DynamicProxyPoolService) RefreshDue(ctx context.Context, now time.Time) error {
	pools, err := s.repo.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("list enabled pools: %w", err)
	}
	for _, m := range pools {
		if m.SourceType != DynamicPoolSourceExtractAPI || strings.TrimSpace(m.ExtractURL) == "" {
			continue
		}
		alive, err := s.pruneAndCount(ctx, m.NamePrefix, now)
		if err != nil {
			continue
		}
		if alive != m.AliveCount {
			_ = s.repo.UpdateAliveCount(ctx, m.ID, alive)
		}
		due := alive < m.MinAlive
		if !due && m.LastExtractAt != nil {
			due = now.Sub(*m.LastExtractAt) >= time.Duration(m.RefreshIntervalSec)*time.Second
		}
		if !due && m.LastExtractAt == nil {
			due = true
		}
		if !due {
			continue
		}
		if _, err := s.doExtract(ctx, m); err != nil {
			log.Printf("[DynamicProxyPool] extract failed pool=%d name=%s: %v", m.ID, m.Name, err)
		}
	}
	return nil
}

// pruneAndCount deletes expired unused proxy rows owned by prefix and returns
// the remaining alive count.
func (s *DynamicProxyPoolService) pruneAndCount(ctx context.Context, prefix string, now time.Time) (int, error) {
	owned, err := s.proxyRepo.ListOwnedByPrefix(ctx, prefix)
	if err != nil {
		return 0, err
	}
	alive := 0
	for _, p := range owned {
		if p.IsExpired(now) {
			if p.AccountCount == 0 {
				_ = s.proxyRepo.Delete(ctx, p.ID)
			}
			continue
		}
		if p.Status == StatusActive {
			alive++
		}
	}
	return alive, nil
}

func (s *DynamicProxyPoolService) doExtract(ctx context.Context, m *DynamicProxyPool) (*DynamicProxyPoolExtractResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.ExtractURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.updateExtractState(ctx, m.ID, "error", err.Error())
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		s.updateExtractState(ctx, m.ID, "error", err.Error())
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncateStr(string(body), 200))
		s.updateExtractState(ctx, m.ID, "error", msg)
		return nil, fmt.Errorf("extract failed: %s", msg)
	}

	// Parse IPs
	endpoints, err := s.parseResponse(m, body)
	if err != nil {
		s.updateExtractState(ctx, m.ID, "error", err.Error())
		return nil, err
	}

	// Create proxy records
	result := &DynamicProxyPoolExtractResult{}
	now := time.Now()
	expiresAt := now.Add(time.Duration(m.IPDurationSec) * time.Second)

	for _, ep := range endpoints {
		proxy := &Proxy{
			Name:           fmt.Sprintf("%s%s:%d", m.NamePrefix, ep.IP, ep.Port),
			Protocol:       m.Protocol,
			Host:           ep.IP,
			Port:           ep.Port,
			Username:       ep.Username,
			Password:       ep.Password,
			Status:         StatusActive,
			ExpiresAt:      &expiresAt,
			FallbackMode:   FallbackModeNone,
			ExpiryWarnDays: 0,
		}
		// Apply fixed auth if configured
		if m.AuthMode == "fixed" {
			proxy.Username = m.Username
			proxy.Password = m.Password
		}

		if err := s.proxyRepo.Create(ctx, proxy); err != nil {
			result.Failed++
			continue
		}
		result.Created++
	}

	s.updateExtractState(ctx, m.ID, "success", "")
	alive, _ := s.pruneAndCount(ctx, m.NamePrefix, time.Now())
	_ = s.repo.UpdateAliveCount(ctx, m.ID, alive)
	result.AliveCount = alive

	return result, nil
}

func (s *DynamicProxyPoolService) parseResponse(m *DynamicProxyPool, body []byte) ([]extractedEndpoint, error) {
	switch strings.ToLower(m.ResponseFormat) {
	case "json":
		return s.parseJSON(m, body)
	default:
		return s.parseTxt(m, body)
	}
}

type extractedEndpoint struct {
	IP       string
	Port     int
	Username string
	Password string
}

// proxyLineRegex matches formats like:
// ip:port
// ip:port:user:pass
// protocol://user:pass@ip:port
var proxyLineRegex = regexp.MustCompile(`^(?:(?:[\w]+)://)?(?:([^:@]+):([^@]+)@)?([^:]+):(\d+)(?::([^:]+):(.+))?$`)

func (s *DynamicProxyPoolService) parseTxt(m *DynamicProxyPool, body []byte) ([]extractedEndpoint, error) {
	sep := m.LineSeparator
	switch sep {
	case "", `\r\n`:
		sep = "\r\n"
	case `\n`:
		sep = "\n"
	}
	content := strings.TrimSpace(string(body))
	lines := strings.Split(content, sep)
	if len(lines) == 1 && sep == "\r\n" {
		// Fallback to \n if \r\n didn't split
		lines = strings.Split(content, "\n")
	}

	var endpoints []extractedEndpoint
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		ep, err := parseProxyLine(line)
		if err != nil {
			continue
		}
		endpoints = append(endpoints, ep)
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no valid IPs found in response")
	}
	return endpoints, nil
}

func parseProxyLine(line string) (extractedEndpoint, error) {
	matches := proxyLineRegex.FindStringSubmatch(line)
	if matches == nil {
		// Try simple ip:port
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			port, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err == nil && port > 0 && port <= 65535 {
				return extractedEndpoint{IP: strings.TrimSpace(parts[0]), Port: port}, nil
			}
		}
		return extractedEndpoint{}, fmt.Errorf("unrecognized format: %s", line)
	}

	ip := matches[3]
	port, _ := strconv.Atoi(matches[4])
	user := matches[1]
	pass := matches[2]
	// Check for ip:port:user:pass format
	if matches[5] != "" && matches[6] != "" {
		user = matches[5]
		pass = matches[6]
	}
	return extractedEndpoint{IP: ip, Port: port, Username: user, Password: pass}, nil
}

func (s *DynamicProxyPoolService) parseJSON(m *DynamicProxyPool, body []byte) ([]extractedEndpoint, error) {
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	ipPath := m.IPFieldPath
	portPath := m.PortFieldPath
	if ipPath == "" {
		ipPath = "ip"
	}
	if portPath == "" {
		portPath = "port"
	}

	// Try to find array in response
	items := findJSONArray(raw, ipPath)
	if items == nil {
		return nil, fmt.Errorf("no array found at path for IP extraction")
	}

	var endpoints []extractedEndpoint
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		ip := jsonString(obj, ipPath)
		port := jsonInt(obj, portPath)
		if ip == "" || port == 0 {
			continue
		}
		endpoints = append(endpoints, extractedEndpoint{
			IP:       ip,
			Port:     port,
			Username: jsonString(obj, "username"),
			Password: jsonString(obj, "password"),
		})
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no valid IPs in JSON response")
	}
	return endpoints, nil
}

func (s *DynamicProxyPoolService) updateExtractState(ctx context.Context, id int64, status, errMsg string) {
	now := time.Now()
	_ = s.repo.UpdateExtractState(ctx, id, status, errMsg, &now)
}

func (s *DynamicProxyPoolService) validate(ctx context.Context, m *DynamicProxyPool, excludeID int64) error {
	if m.Name == "" {
		return errors.BadRequest("POOL_NAME_REQUIRED", "name is required")
	}
	if m.SourceType == DynamicPoolSourceExtractAPI && m.ExtractURL == "" {
		return errors.BadRequest("POOL_URL_REQUIRED", "extract_url is required for extract_api source")
	}
	// Check name_prefix uniqueness
	exists, err := s.repo.ExistsNamePrefix(ctx, m.NamePrefix, excludeID)
	if err != nil {
		return err
	}
	if exists {
		return errors.Conflict("POOL_PREFIX_CONFLICT", "name_prefix already in use")
	}
	return nil
}

// --- Helpers ---

func coalesce(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}

func sanitizePrefix(name string) string {
	// Keep only alphanumeric and dash, lowercase, max 20 chars
	result := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return '-'
	}, name)
	if len(result) > 20 {
		result = result[:20]
	}
	return strings.Trim(result, "-")
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func findJSONArray(v any, _ string) []any {
	// Simple: if top-level is array, use it; if map, look for "data" key
	switch val := v.(type) {
	case []any:
		return val
	case map[string]any:
		if d, ok := val["data"]; ok {
			if arr, ok2 := d.([]any); ok2 {
				return arr
			}
		}
		// Try first array field
		for _, field := range val {
			if arr, ok := field.([]any); ok {
				return arr
			}
		}
	}
	return nil
}

func jsonString(obj map[string]any, key string) string {
	v, ok := obj[key]
	if !ok {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	case float64:
		return fmt.Sprintf("%v", s)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func jsonInt(obj map[string]any, key string) int {
	v, ok := obj[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	default:
		return 0
	}
}

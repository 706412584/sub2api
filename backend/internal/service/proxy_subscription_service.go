package service

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/clashsub"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	maxSubscriptionBodyBytes = 16 << 20
	minSyncIntervalSec       = 60
	maxSyncIntervalSec       = 86400
	defaultSyncIntervalSec   = 300
	defaultBasePort          = 21080
	defaultMaxPorts          = 10
	// Practical upper bound for local mihomo listeners (was 64; UI often set 20).
	maxMaxPorts = 500
)

// ProxySubscriptionService manages CRUD + sync for embedded subscription sources.
type ProxySubscriptionService struct {
	repo              ProxySubscriptionRepository
	proxyRepo         ProxyRepository
	engine            *MihomoEngine
	httpClient        *http.Client
	allowInsecureSub  bool
	allowNonLocalBind bool
}

// NewProxySubscriptionService constructs the service.
func NewProxySubscriptionService(
	repo ProxySubscriptionRepository,
	proxyRepo ProxyRepository,
	engine *MihomoEngine,
) *ProxySubscriptionService {
	// Subscriptions are frequently hosted on bare IPs whose certificates do not
	// match the IP SAN (or use self-signed certs). Skip upstream certificate
	// verification so these sources remain fetchable; the response is still
	// transport-level TLS (encrypted) and limited to subscription fetch only.
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // subscriptions may be bare-IP HTTPS
	}
	return &ProxySubscriptionService{
		repo:      repo,
		proxyRepo: proxyRepo,
		engine:    engine,
		httpClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
	}
}

// SetAllowInsecureSubscription allows non-localhost http subscription URLs.
func (s *ProxySubscriptionService) SetAllowInsecureSubscription(v bool) {
	if s != nil {
		s.allowInsecureSub = v
	}
}

// SetAllowNonLocalBind allows non-loopback bind addresses.
func (s *ProxySubscriptionService) SetAllowNonLocalBind(v bool) {
	if s != nil {
		s.allowNonLocalBind = v
	}
}

// Create validates and persists a new source.
func (s *ProxySubscriptionService) Create(ctx context.Context, p ProxySubscriptionCreateParams) (*ProxySubscription, error) {
	m, err := s.buildFromCreate(p)
	if err != nil {
		return nil, err
	}
	if err := s.validateModel(ctx, m, 0); err != nil {
		return nil, err
	}
	// Due immediately when enabled.
	if m.Enabled {
		now := time.Now()
		m.NextDueAt = &now
	}
	if err := s.repo.Create(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

// Get returns one source.
func (s *ProxySubscriptionService) Get(ctx context.Context, id int64) (*ProxySubscription, error) {
	return s.repo.GetByID(ctx, id)
}

// List returns paginated sources.
func (s *ProxySubscriptionService) List(ctx context.Context, params ProxySubscriptionListParams) ([]*ProxySubscription, int64, error) {
	return s.repo.List(ctx, params)
}

// Update patches a source.
func (s *ProxySubscriptionService) Update(ctx context.Context, id int64, p ProxySubscriptionUpdateParams) (*ProxySubscription, error) {
	m, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.Name != nil {
		m.Name = strings.TrimSpace(*p.Name)
	}
	if p.Enabled != nil {
		m.Enabled = *p.Enabled
	}
	if p.SourceType != nil {
		m.SourceType = strings.TrimSpace(*p.SourceType)
	}
	if p.SubscriptionURL != nil {
		m.SubscriptionURL = strings.TrimSpace(*p.SubscriptionURL)
	}
	if p.InlineBody != nil {
		m.InlineBody = *p.InlineBody
	}
	if p.NamePrefix != nil {
		m.NamePrefix = strings.TrimSpace(*p.NamePrefix)
	}
	if p.Protocol != nil {
		m.Protocol = strings.ToLower(strings.TrimSpace(*p.Protocol))
	}
	if p.BindAddress != nil {
		m.BindAddress = strings.TrimSpace(*p.BindAddress)
	}
	if p.BasePort != nil {
		m.BasePort = *p.BasePort
	}
	if p.MaxPorts != nil {
		m.MaxPorts = *p.MaxPorts
	}
	if p.SyncIntervalSec != nil {
		m.SyncIntervalSec = *p.SyncIntervalSec
	}
	if p.NodeAllowContains != nil {
		m.NodeAllowContains = *p.NodeAllowContains
	}
	if p.NodeIdentityAllowlist != nil {
		m.NodeIdentityAllowlist = *p.NodeIdentityAllowlist
	}
	if err := s.validateModel(ctx, m, id); err != nil {
		return nil, err
	}
	if m.Enabled {
		now := time.Now()
		m.NextDueAt = &now
	} else {
		m.NextDueAt = nil
		if s.engine != nil {
			s.engine.StopSource(id)
		}
	}
	if err := s.repo.Update(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

// Delete removes a source and stops its engine. Owned proxies are left for manual prune or next sync of another source.
func (s *ProxySubscriptionService) Delete(ctx context.Context, id int64) error {
	if _, err := s.repo.GetByID(ctx, id); err != nil {
		return err
	}
	if s.engine != nil {
		s.engine.StopSource(id)
	}
	return s.repo.Delete(ctx, id)
}

// PreviewNodes fetches and parses an existing source without writing proxies/engine.
func (s *ProxySubscriptionService) PreviewNodes(ctx context.Context, id int64) (*ProxySubscriptionPreviewResult, error) {
	m, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	res, err := s.previewFromModel(ctx, m)
	if err != nil {
		return nil, err
	}
	res.SelectedIdentities = append([]string(nil), m.NodeIdentityAllowlist...)
	return res, nil
}

// PreviewNodesDraft fetches and parses unsaved source fields without writing proxies/engine.
func (s *ProxySubscriptionService) PreviewNodesDraft(ctx context.Context, p ProxySubscriptionPreviewParams) (*ProxySubscriptionPreviewResult, error) {
	m, err := s.buildFromCreate(ProxySubscriptionCreateParams{
		Name:              "preview",
		SourceType:        p.SourceType,
		SubscriptionURL:   p.SubscriptionURL,
		InlineBody:        p.InlineBody,
		NodeAllowContains: p.NodeAllowContains,
	})
	if err != nil {
		return nil, err
	}
	// Skip full validateModel (prefix uniqueness etc.); only need fetchable source.
	st := strings.ToLower(strings.TrimSpace(m.SourceType))
	switch st {
	case ProxySubscriptionSourceURL, ProxySubscriptionSourceInline:
		m.SourceType = st
	default:
		return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_SOURCE_TYPE", "source_type must be url or inline")
	}
	if st == ProxySubscriptionSourceURL && strings.TrimSpace(m.SubscriptionURL) == "" {
		return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_URL_REQUIRED", "subscription_url is required for url source")
	}
	if st == ProxySubscriptionSourceInline && strings.TrimSpace(m.InlineBody) == "" {
		return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_EMPTY_BODY", "inline_body is required for inline source")
	}
	return s.previewFromModel(ctx, m)
}

func (s *ProxySubscriptionService) previewFromModel(ctx context.Context, m *ProxySubscription) (*ProxySubscriptionPreviewResult, error) {
	body, err := s.fetchBody(ctx, m)
	if err != nil {
		return nil, err
	}
	nodes, err := clashsub.ParseSubscription(body)
	if err != nil {
		return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_PARSE", err.Error())
	}
	// Apply optional name filter for display, but do not apply identity allowlist
	// so the UI can still re-select nodes.
	if len(m.NodeAllowContains) > 0 {
		filtered, err := clashsub.SelectNodes(nodes, len(nodes), m.NodeAllowContains, nil)
		if err != nil {
			return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_FILTER", err.Error())
		}
		nodes = filtered
	}
	out := make([]ProxySubscriptionPreviewNode, 0, len(nodes))
	for _, n := range nodes {
		server := ""
		port := ""
		if n.Raw != nil {
			if v, ok := n.Raw["server"]; ok {
				server = fmt.Sprintf("%v", v)
			}
			if server == "" {
				if v, ok := n.Raw["servername"]; ok {
					server = fmt.Sprintf("%v", v)
				}
			}
			if v, ok := n.Raw["port"]; ok {
				port = fmt.Sprintf("%v", v)
			}
		}
		out = append(out, ProxySubscriptionPreviewNode{
			Identity: n.Identity,
			Name:     n.Name,
			Type:     n.Type,
			Server:   server,
			Port:     port,
		})
	}
	return &ProxySubscriptionPreviewResult{
		Nodes:              out,
		Total:              len(out),
		SelectedIdentities: []string{},
	}, nil
}

// SyncNow runs one full sync for a source.
func (s *ProxySubscriptionService) SyncNow(ctx context.Context, id int64) (*ProxySubscriptionSyncResult, error) {
	m, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.syncOne(ctx, m)
}

// SyncAllEnabled runs sync for every enabled source (used by runner / manual ops).
func (s *ProxySubscriptionService) SyncAllEnabled(ctx context.Context) error {
	list, err := s.repo.ListEnabled(ctx)
	if err != nil {
		return err
	}
	var first error
	for _, m := range list {
		if _, err := s.syncOne(ctx, m); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ReconcileEngines 用已落盘的 mihomo 配置拉起进程（服务启动时调用）。
// 服务重启后 MihomoEngine.procs 是空的，而订阅源要等 next_due_at 到期才会
// 重新 sync——期间所有 sidecar 端口没人监听，代理全部不可用，只能手动点同步。
// 这里不重新拉订阅（避免启动依赖外部源）：config.yaml 在上次 sync 已落盘，
// 直接用 DB 里的 LastConfigHash 调 EnsureRunning 拉起进程；config 缺失的源
// 走一次完整 syncOne 兜底。
func (s *ProxySubscriptionService) ReconcileEngines(ctx context.Context) {
	if s == nil || s.engine == nil {
		return
	}
	list, err := s.repo.ListEnabled(ctx)
	if err != nil {
		log.Printf("[ProxySubscription] startup reconcile: list enabled failed: %v", err)
		return
	}
	started, missing := 0, 0
	for _, m := range list {
		cfgPath := s.engine.ConfigPathFor(m.ID)
		if st, err := os.Stat(cfgPath); err != nil || st.IsDir() || m.LastConfigHash == "" {
			// 配置从未落盘：做一次完整同步（拉订阅 + 写配置 + 起进程）
			if _, err := s.syncOne(ctx, m); err != nil {
				log.Printf("[ProxySubscription] startup reconcile: source %d(%s) initial sync failed: %v", m.ID, m.Name, err)
			} else {
				started++
			}
			missing++
			continue
		}
		if err := s.engine.EnsureRunning(m.ID, m.NamePrefix, m.BindAddress, nil, m.LastConfigHash); err != nil {
			log.Printf("[ProxySubscription] startup reconcile: source %d(%s) ensure running failed: %v", m.ID, m.Name, err)
			continue
		}
		started++
	}
	if len(list) > 0 {
		log.Printf("[ProxySubscription] startup reconcile done: %d/%d engine sources running (%d needed initial sync)", started, len(list), missing)
	}
}

// SyncDue runs due sources (next_due_at <= now or nil).
func (s *ProxySubscriptionService) SyncDue(ctx context.Context, now time.Time, limit int) error {
	list, err := s.repo.ListDue(ctx, now, limit)
	if err != nil {
		return err
	}
	var first error
	for _, m := range list {
		if _, err := s.syncOne(ctx, m); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// EngineStatus reports binary + running processes.
func (s *ProxySubscriptionService) EngineStatus(ctx context.Context) (*ProxySubscriptionEngineStatus, error) {
	bin, found := "", false
	dataDir := ""
	running := 0
	snap := map[int64]ProxySubscriptionEngineSourceStatus{}
	if s.engine != nil {
		bin, found = s.engine.ResolveBinary()
		dataDir = s.engine.DataDir()
		running = s.engine.RunningCount()
		snap = s.engine.StatusSnapshot()
	}
	list, _, err := s.repo.List(ctx, ProxySubscriptionListParams{Page: 1, PageSize: 200})
	if err != nil {
		return nil, err
	}
	sources := make([]ProxySubscriptionEngineSourceStatus, 0, len(list))
	for _, m := range list {
		st := ProxySubscriptionEngineSourceStatus{
			ID:         m.ID,
			Name:       m.Name,
			NamePrefix: m.NamePrefix,
			BasePort:   m.BasePort,
			MaxPorts:   m.MaxPorts,
			ConfigHash: m.LastConfigHash,
		}
		if runtime, ok := snap[m.ID]; ok {
			st.Running = runtime.Running
			if runtime.ConfigHash != "" {
				st.ConfigHash = runtime.ConfigHash
			}
			st.LastError = runtime.LastError
		}
		sources = append(sources, st)
	}
	return &ProxySubscriptionEngineStatus{
		BinaryPath:   bin,
		BinaryFound:  found,
		DataDir:      dataDir,
		RunningCount: running,
		Sources:      sources,
	}, nil
}

func (s *ProxySubscriptionService) syncOne(ctx context.Context, m *ProxySubscription) (*ProxySubscriptionSyncResult, error) {
	now := time.Now()
	_ = s.repo.UpdateSyncState(ctx, m.ID, ProxySubscriptionStatusRunning, "", m.LastConfigHash, m.DesiredCount, nil, m.NextDueAt)

	body, err := s.fetchBody(ctx, m)
	if err != nil {
		s.markSyncError(ctx, m, now, err)
		return nil, err
	}
	nodes, err := clashsub.ParseSubscription(body)
	if err != nil {
		s.markSyncError(ctx, m, now, fmt.Errorf("parse: %w", err))
		return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_PARSE", err.Error())
	}
	bindings, err := clashsub.BuildBindings(
		nodes,
		m.NamePrefix,
		m.BindAddress,
		m.Protocol,
		m.BasePort,
		m.MaxPorts,
		m.NodeAllowContains,
		m.NodeIdentityAllowlist,
	)
	if err != nil {
		s.markSyncError(ctx, m, now, fmt.Errorf("bind: %w", err))
		return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_BIND", err.Error())
	}
	if len(bindings) == 0 {
		err := fmt.Errorf("no bindings produced")
		s.markSyncError(ctx, m, now, err)
		return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_EMPTY", err.Error())
	}

	res := &ProxySubscriptionSyncResult{DesiredCount: len(bindings)}
	cfgPath := ""
	if s.engine != nil {
		cfgPath = s.engine.ConfigPathFor(m.ID)
	} else {
		cfgPath = filepath.Join("data", "proxy-subscriptions", fmt.Sprintf("source-%d", m.ID), "config.yaml")
	}
	hash, err := clashsub.WriteMihomoConfig(cfgPath, m.BindAddress, m.NamePrefix, bindings)
	if err != nil {
		s.markSyncError(ctx, m, now, fmt.Errorf("write config: %w", err))
		return nil, err
	}
	res.ConfigHash = hash

	// Upsert proxies first so gateway can use them even if engine start fails.
	desired := clashsub.EndpointsFromBindings(bindings)
	if err := s.applyProxies(ctx, m.NamePrefix, desired, res); err != nil {
		s.markSyncError(ctx, m, now, err)
		return res, err
	}

	engineErr := error(nil)
	if s.engine != nil {
		if err := s.engine.EnsureRunning(m.ID, m.NamePrefix, m.BindAddress, bindings, hash); err != nil {
			engineErr = err
			res.EngineSkipped = true
			res.Message = err.Error()
		} else {
			res.EngineRunning = true
		}
	} else {
		res.EngineSkipped = true
		res.Message = "mihomo engine not configured"
	}

	next := now.Add(time.Duration(m.SyncIntervalSec) * time.Second)
	status := ProxySubscriptionStatusOK
	errMsg := ""
	if engineErr != nil {
		// Proxies written but engine failed — surface as error so UI shows it.
		status = ProxySubscriptionStatusError
		errMsg = engineErr.Error()
	}
	if err := s.repo.UpdateSyncState(ctx, m.ID, status, errMsg, hash, len(desired), &now, &next); err != nil {
		return res, err
	}
	if engineErr != nil {
		return res, engineErr
	}
	return res, nil
}

func (s *ProxySubscriptionService) applyProxies(ctx context.Context, prefix string, desired []clashsub.LocalEndpoint, res *ProxySubscriptionSyncResult) error {
	existing, err := s.proxyRepo.ListOwnedByPrefix(ctx, prefix)
	if err != nil {
		return fmt.Errorf("list owned proxies: %w", err)
	}
	byName := map[string]ProxyWithAccountCount{}
	// 按 {prefix}{hash8} 索引：hash8 只由节点 identity 决定，代理名的可读片段
	// 规则变化（如开始保留中文）后仍能认出同一节点，走重命名而不是删旧建新，
	// 避免已绑定账号的代理被重建。
	byIdentity := map[string]ProxyWithAccountCount{}
	for _, p := range existing {
		if prev, ok := byName[p.Name]; ok {
			if p.ID < prev.ID {
				byName[p.Name] = p
			}
			continue
		}
		byName[p.Name] = p
		if key := clashsub.ProxyNameIdentityKey(prefix, p.Name); key != "" {
			if prev, ok := byIdentity[key]; !ok || p.ID < prev.ID {
				byIdentity[key] = p
			}
		}
	}
	desiredNames := map[string]clashsub.LocalEndpoint{}
	renamedIDs := map[int64]struct{}{}
	for _, ep := range desired {
		desiredNames[ep.Name] = ep
		cur, ok := byName[ep.Name]
		if !ok {
			// 名字没命中：可读片段规则变了但节点未变时，按身份 hash 复用旧行重命名。
			if key := clashsub.ProxyNameIdentityKey(prefix, ep.Name); key != "" {
				if prior, found := byIdentity[key]; found {
					if _, dup := desiredNames[prior.Name]; !dup {
						cur, ok = prior, true
						renamedIDs[prior.ID] = struct{}{}
					}
				}
			}
		}
		if ok {
			needUpdate := cur.Name != ep.Name || cur.Host != ep.Host || cur.Port != ep.Port ||
				cur.Protocol != ep.Protocol || cur.Status != StatusActive
			if needUpdate {
				// Preserve expiry/fallback fields to avoid UpdateProxy zeroing.
				cur.Host = ep.Host
				cur.Port = ep.Port
				cur.Protocol = ep.Protocol
				cur.Status = StatusActive
				cur.Name = ep.Name
				if err := s.proxyRepo.Update(ctx, &cur.Proxy); err != nil {
					return fmt.Errorf("update proxy %s: %w", ep.Name, err)
				}
				res.Updated++
			} else {
				res.Unchanged++
			}
			continue
		}
		p := &Proxy{
			Name:           ep.Name,
			Protocol:       ep.Protocol,
			Host:           ep.Host,
			Port:           ep.Port,
			Status:         StatusActive,
			FallbackMode:   FallbackModeNone,
			ExpiryWarnDays: 0,
		}
		if err := s.proxyRepo.Create(ctx, p); err != nil {
			return fmt.Errorf("create proxy %s: %w", ep.Name, err)
		}
		res.Created++
	}

	keepID := map[string]int64{}
	for name, p := range byName {
		keepID[name] = p.ID
	}
	for _, p := range existing {
		if _, renamed := renamedIDs[p.ID]; renamed {
			continue
		}
		primary := keepID[p.Name]
		isDup := primary != 0 && p.ID != primary
		_, wanted := desiredNames[p.Name]
		if wanted && !isDup {
			continue
		}
		if p.AccountCount > 0 {
			res.Skipped++
			continue
		}
		if err := s.proxyRepo.Delete(ctx, p.ID); err != nil {
			return fmt.Errorf("delete proxy %s(%d): %w", p.Name, p.ID, err)
		}
		res.Deleted++
	}
	return nil
}

func (s *ProxySubscriptionService) fetchBody(ctx context.Context, m *ProxySubscription) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(m.SourceType)) {
	case ProxySubscriptionSourceInline, "body", "text":
		body := strings.TrimSpace(m.InlineBody)
		if body == "" {
			return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_EMPTY_BODY", "inline_body is empty")
		}
		if len(body) > maxSubscriptionBodyBytes {
			return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_BODY_TOO_LARGE", "inline_body exceeds 16MiB")
		}
		return []byte(body), nil
	default:
		rawURL := strings.TrimSpace(m.SubscriptionURL)
		if rawURL == "" {
			return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_URL_REQUIRED", "subscription_url is required")
		}
		u, err := url.Parse(rawURL)
		if err != nil {
			return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_URL_INVALID", "invalid subscription_url")
		}
		switch strings.ToLower(u.Scheme) {
		case "https":
			// ok
		case "http":
			host := u.Hostname()
			if !s.allowInsecureSub && host != "127.0.0.1" && host != "localhost" && host != "::1" {
				return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_HTTP_DENIED", "http subscription URL only allowed for localhost")
			}
		case "file":
			// Local file path for ops/testing; path from URL.
			path := u.Path
			if path == "" {
				return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_FILE_INVALID", "file url missing path")
			}
			// Windows file:///C:/... → /C:/...
			if strings.HasPrefix(path, "/") && len(path) > 2 && path[2] == ':' {
				path = path[1:]
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read file subscription: %w", err)
			}
			if len(b) > maxSubscriptionBodyBytes {
				return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_BODY_TOO_LARGE", "subscription body exceeds 16MiB")
			}
			return b, nil
		default:
			return nil, infraerrors.BadRequest("PROXY_SUBSCRIPTION_SCHEME", fmt.Sprintf("unsupported scheme %q", u.Scheme))
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "sub2api-proxy-subscription/0.1")
		resp, err := s.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch subscription: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return nil, fmt.Errorf("fetch subscription HTTP %d: %s", resp.StatusCode, string(b))
		}
		return io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionBodyBytes))
	}
}

func (s *ProxySubscriptionService) markSyncError(ctx context.Context, m *ProxySubscription, now time.Time, err error) {
	next := now.Add(time.Duration(m.SyncIntervalSec) * time.Second)
	msg := ""
	if err != nil {
		msg = err.Error()
		if len(msg) > 2000 {
			msg = msg[:2000]
		}
	}
	_ = s.repo.UpdateSyncState(ctx, m.ID, ProxySubscriptionStatusError, msg, m.LastConfigHash, m.DesiredCount, &now, &next)
}

func (s *ProxySubscriptionService) buildFromCreate(p ProxySubscriptionCreateParams) (*ProxySubscription, error) {
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	sourceType := strings.TrimSpace(p.SourceType)
	if sourceType == "" {
		if strings.TrimSpace(p.InlineBody) != "" && strings.TrimSpace(p.SubscriptionURL) == "" {
			sourceType = ProxySubscriptionSourceInline
		} else {
			sourceType = ProxySubscriptionSourceURL
		}
	}
	prefix := strings.TrimSpace(p.NamePrefix)
	if prefix == "" {
		prefix = "sidecar-a-"
	}
	protocol := strings.ToLower(strings.TrimSpace(p.Protocol))
	if protocol == "" {
		protocol = "socks5"
	}
	bind := strings.TrimSpace(p.BindAddress)
	if bind == "" {
		bind = "127.0.0.1"
	}
	basePort := p.BasePort
	if basePort == 0 {
		basePort = defaultBasePort
	}
	maxPorts := p.MaxPorts
	if maxPorts == 0 {
		maxPorts = defaultMaxPorts
	}
	interval := p.SyncIntervalSec
	if interval == 0 {
		interval = defaultSyncIntervalSec
	}
	return &ProxySubscription{
		Name:                  strings.TrimSpace(p.Name),
		Enabled:               enabled,
		SourceType:            sourceType,
		SubscriptionURL:       strings.TrimSpace(p.SubscriptionURL),
		InlineBody:            p.InlineBody,
		NamePrefix:            prefix,
		Protocol:              protocol,
		BindAddress:           bind,
		BasePort:              basePort,
		MaxPorts:              maxPorts,
		SyncIntervalSec:       interval,
		NodeAllowContains:     append([]string(nil), p.NodeAllowContains...),
		NodeIdentityAllowlist: append([]string(nil), p.NodeIdentityAllowlist...),
		CreatedBy:             p.CreatedBy,
	}, nil
}

func (s *ProxySubscriptionService) validateModel(ctx context.Context, m *ProxySubscription, excludeID int64) error {
	if m == nil {
		return ErrProxySubscriptionInvalid
	}
	m.NodeAllowContains = normalizeStringList(m.NodeAllowContains, 64, 128)
	m.NodeIdentityAllowlist = normalizeStringList(m.NodeIdentityAllowlist, maxMaxPorts, 256)
	if strings.TrimSpace(m.Name) == "" {
		return infraerrors.BadRequest("PROXY_SUBSCRIPTION_NAME", "name is required")
	}
	if len(m.Name) > 100 {
		return infraerrors.BadRequest("PROXY_SUBSCRIPTION_NAME", "name too long")
	}
	st := strings.ToLower(strings.TrimSpace(m.SourceType))
	switch st {
	case ProxySubscriptionSourceURL, ProxySubscriptionSourceInline:
		m.SourceType = st
	default:
		return infraerrors.BadRequest("PROXY_SUBSCRIPTION_SOURCE_TYPE", "source_type must be url or inline")
	}
	if st == ProxySubscriptionSourceURL && strings.TrimSpace(m.SubscriptionURL) == "" {
		return infraerrors.BadRequest("PROXY_SUBSCRIPTION_URL_REQUIRED", "subscription_url is required for url source")
	}
	if st == ProxySubscriptionSourceInline && strings.TrimSpace(m.InlineBody) == "" {
		return infraerrors.BadRequest("PROXY_SUBSCRIPTION_EMPTY_BODY", "inline_body is required for inline source")
	}
	if !strings.HasPrefix(m.NamePrefix, ManagedProxyNamePrefix) {
		return infraerrors.BadRequest("PROXY_SUBSCRIPTION_PREFIX", "name_prefix must start with sidecar-")
	}
	if len(m.NamePrefix) > 40 {
		return infraerrors.BadRequest("PROXY_SUBSCRIPTION_PREFIX", "name_prefix too long")
	}
	switch m.Protocol {
	case "socks5", "socks5h", "http", "https":
	default:
		return infraerrors.BadRequest("PROXY_SUBSCRIPTION_PROTOCOL", "protocol must be socks5|socks5h|http|https")
	}
	if err := validateLoopbackBind(m.BindAddress, s.allowNonLocalBind); err != nil {
		return infraerrors.BadRequest("PROXY_SUBSCRIPTION_BIND", err.Error())
	}
	if m.BasePort < 1024 || m.BasePort > 65535 {
		return infraerrors.BadRequest("PROXY_SUBSCRIPTION_PORT", "base_port out of range")
	}
	if m.MaxPorts < 1 || m.MaxPorts > maxMaxPorts {
		return infraerrors.BadRequest("PROXY_SUBSCRIPTION_MAX_PORTS", fmt.Sprintf("max_ports must be 1..%d", maxMaxPorts))
	}
	if m.BasePort+m.MaxPorts-1 > 65535 {
		return infraerrors.BadRequest("PROXY_SUBSCRIPTION_PORT_RANGE", "base_port+max_ports exceeds 65535")
	}
	if m.SyncIntervalSec < minSyncIntervalSec || m.SyncIntervalSec > maxSyncIntervalSec {
		return infraerrors.BadRequest("PROXY_SUBSCRIPTION_INTERVAL", fmt.Sprintf("sync_interval_sec must be %d..%d", minSyncIntervalSec, maxSyncIntervalSec))
	}
	// Prefix uniqueness among sources.
	exists, err := s.repo.ExistsNamePrefix(ctx, m.NamePrefix, excludeID)
	if err != nil {
		return err
	}
	if exists {
		return infraerrors.Conflict("PROXY_SUBSCRIPTION_PREFIX_TAKEN", "name_prefix already used by another subscription")
	}
	// Port range overlap with other sources.
	others, _, err := s.repo.List(ctx, ProxySubscriptionListParams{Page: 1, PageSize: 500})
	if err != nil {
		return err
	}
	myLo, myHi := m.BasePort, m.BasePort+m.MaxPorts-1
	for _, o := range others {
		if o.ID == excludeID || o.ID == m.ID {
			continue
		}
		lo, hi := o.BasePort, o.BasePort+o.MaxPorts-1
		if myLo <= hi && lo <= myHi {
			return infraerrors.Conflict("PROXY_SUBSCRIPTION_PORT_OVERLAP", fmt.Sprintf("port range overlaps with subscription %q (%d-%d)", o.Name, lo, hi))
		}
	}
	return nil
}

// TestNode performs a TCP dial test to the node's server:port and returns
// connectivity result with latency. This is a lightweight liveness check
// for raw clash nodes (VLESS/Trojan/SS) that cannot be used as HTTP proxies.
func (s *ProxySubscriptionService) TestNode(ctx context.Context, node ProxySubscriptionPreviewNode) *ProxyNodeTestResult {
	addr := net.JoinHostPort(node.Server, node.Port)
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return &ProxyNodeTestResult{
			Success: false,
			Message: err.Error(),
		}
	}
	_ = conn.Close()
	latency := time.Since(start).Milliseconds()
	return &ProxyNodeTestResult{
		Success:   true,
		Message:   "connected",
		LatencyMs: latency,
	}
}

func validateLoopbackBind(addr string, allowNonLocal bool) error {
	if allowNonLocal {
		return nil
	}
	host := strings.TrimSpace(addr)
	if host == "" {
		return fmt.Errorf("bind_address is required")
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("bind_address %q must be loopback unless non-local bind is allowed", addr)
}

// MaskSubscriptionURL redacts credentials/query for API responses.
func MaskSubscriptionURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		if len(raw) <= 16 {
			return raw
		}
		return raw[:8] + "…(redacted)"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	// Keep scheme/host/path only; path may still be sensitive — truncate.
	s := u.String()
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

func normalizeStringList(in []string, maxItems, maxLen int) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if maxLen > 0 && len(s) > maxLen {
			s = s[:maxLen]
		}
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
		if maxItems > 0 && len(out) >= maxItems {
			break
		}
	}
	return out
}

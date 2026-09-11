package sync

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/subscription-sidecar/internal/adminapi"
	"github.com/Wei-Shaw/sub2api/subscription-sidecar/internal/clash"
	"github.com/Wei-Shaw/sub2api/subscription-sidecar/internal/config"
	"github.com/Wei-Shaw/sub2api/subscription-sidecar/internal/engine"
)

const maxSubscriptionBytes = 16 << 20

type Fetcher interface {
	Fetch(ctx context.Context) ([]byte, error)
}

type FileFetcher struct {
	Path string
}

func (f FileFetcher) Fetch(ctx context.Context) ([]byte, error) {
	return os.ReadFile(f.Path)
}

type HTTPFetcher struct {
	URL                string
	Client             *http.Client
	AllowInsecureLocal bool
}

func (f HTTPFetcher) Fetch(ctx context.Context) ([]byte, error) {
	u, err := url.Parse(f.URL)
	if err != nil {
		return nil, fmt.Errorf("subscription url: parse failed")
	}
	if err := validateSubscriptionURL(u, f.AllowInsecureLocal); err != nil {
		return nil, err
	}

	client := f.Client
	if client == nil {
		client = &http.Client{
			Timeout: 60 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return fmt.Errorf("too many redirects")
				}
				if err := validateSubscriptionURL(req.URL, f.AllowInsecureLocal); err != nil {
					return fmt.Errorf("redirect rejected: %w", err)
				}
				return nil
			},
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("subscription request: create failed")
	}
	req.Header.Set("User-Agent", "sub2api-subscription-sidecar/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, sanitizeFetchError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("fetch subscription HTTP %d: %s", resp.StatusCode, string(b))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionBytes))
	if err != nil {
		return nil, fmt.Errorf("read subscription body: %w", err)
	}
	return data, nil
}

func validateSubscriptionURL(u *url.URL, allowInsecureLocal bool) error {
	if u == nil {
		return fmt.Errorf("subscription url is empty")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if allowInsecureLocal {
			return nil
		}
		host := u.Hostname()
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			return fmt.Errorf("http subscription URL only allowed for localhost unless SIDECAR_ALLOW_INSECURE_SUBSCRIPTION=1")
		}
		return nil
	default:
		return fmt.Errorf("unsupported subscription scheme %q", u.Scheme)
	}
}

// sanitizeFetchError strips URL query/credentials from transport errors so
// subscription tokens are not written to logs.
func sanitizeFetchError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if idx := strings.Index(msg, "?"); idx >= 0 {
		// Drop query string and anything that looks like a full URL tail.
		msg = msg[:idx] + "?[redacted]"
	}
	msg = strings.ReplaceAll(msg, "\n", " ")
	return fmt.Errorf("fetch subscription failed: %s", msg)
}

type AdminClient interface {
	ListOwnedProxies(ctx context.Context, namePrefix string) ([]adminapi.Proxy, error)
	CreateProxy(ctx context.Context, req adminapi.CreateProxyRequest) (*adminapi.Proxy, error)
	UpdateProxy(ctx context.Context, id int64, req adminapi.UpdateProxyRequest) (*adminapi.Proxy, error)
	DeleteProxy(ctx context.Context, id int64) error
}

type Result struct {
	Desired    []engine.LocalEndpoint
	ConfigHash string
	Created    int
	Updated    int
	Unchanged  int
	Deleted    int
	Skipped    int
}

// Service performs one sync cycle: fetch → parse → bind ports → write mihomo config → upsert proxies → prune.
type Service struct {
	Cfg           *config.Config
	Fetcher       Fetcher
	Admin         AdminClient
	AllowContains []string
}

func NewService(cfg *config.Config, admin AdminClient) (*Service, error) {
	var fetcher Fetcher
	switch {
	case cfg.SubscriptionFile != "":
		fetcher = FileFetcher{Path: cfg.SubscriptionFile}
	case cfg.SubscriptionURL != "":
		fetcher = HTTPFetcher{
			URL:                cfg.SubscriptionURL,
			AllowInsecureLocal: cfg.AllowInsecureSubscription,
		}
	default:
		return nil, fmt.Errorf("no subscription source")
	}
	return &Service{Cfg: cfg, Fetcher: fetcher, Admin: admin}, nil
}

func (s *Service) RunOnce(ctx context.Context) (*Result, error) {
	body, err := s.Fetcher.Fetch(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	nodes, err := clash.ParseSubscription(body)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	bindings, err := engine.BuildBindings(
		nodes,
		s.Cfg.NamePrefix,
		s.Cfg.BindAddress,
		s.Cfg.Protocol,
		s.Cfg.BasePort,
		s.Cfg.MaxPorts,
		s.AllowContains,
	)
	if err != nil {
		return nil, fmt.Errorf("bind: %w", err)
	}
	if len(bindings) == 0 {
		return nil, fmt.Errorf("no bindings produced")
	}

	hash, err := engine.WriteMihomoConfig(s.Cfg.MihomoConfigPath, s.Cfg.BindAddress, s.Cfg.NamePrefix, bindings)
	if err != nil {
		return nil, fmt.Errorf("write mihomo config: %w", err)
	}

	desired := engine.EndpointsFromBindings(bindings)
	res := &Result{Desired: desired, ConfigHash: hash}

	if s.Cfg.DryRun || s.Admin == nil {
		res.Skipped = len(desired)
		return res, nil
	}

	existing, err := s.Admin.ListOwnedProxies(ctx, s.Cfg.NamePrefix)
	if err != nil {
		return nil, fmt.Errorf("list proxies: %w", err)
	}

	byName := map[string]adminapi.Proxy{}
	for _, p := range existing {
		// If duplicates exist, keep the lowest id and try to prune extras later.
		if prev, ok := byName[p.Name]; ok {
			if p.ID < prev.ID {
				byName[p.Name] = p
			}
			continue
		}
		byName[p.Name] = p
	}

	desiredNames := map[string]engine.LocalEndpoint{}
	for _, ep := range desired {
		desiredNames[ep.Name] = ep
		if cur, ok := byName[ep.Name]; ok {
			needUpdate := cur.Host != ep.Host || cur.Port != ep.Port || cur.Protocol != ep.Protocol || cur.Status != "active"
			if needUpdate {
				// WARNING: Sub2API full Update resets expiry/fallback to zero-values.
				// Sidecar-managed proxies must not use fallback/expiry features.
				_, err := s.Admin.UpdateProxy(ctx, cur.ID, adminapi.UpdateProxyRequest{
					Name:     ep.Name,
					Protocol: ep.Protocol,
					Host:     ep.Host,
					Port:     ep.Port,
					Status:   "active",
				})
				if err != nil {
					return res, fmt.Errorf("update proxy %s: %w", ep.Name, err)
				}
				res.Updated++
			} else {
				res.Unchanged++
			}
			continue
		}
		_, err := s.Admin.CreateProxy(ctx, adminapi.CreateProxyRequest{
			Name:     ep.Name,
			Protocol: ep.Protocol,
			Host:     ep.Host,
			Port:     ep.Port,
		})
		if err != nil {
			return res, fmt.Errorf("create proxy %s: %w", ep.Name, err)
		}
		res.Created++
	}

	// Prune owned proxies that are no longer desired and not bound to accounts.
	// Also prune duplicate names beyond the chosen primary id.
	seenKeepID := map[string]int64{}
	for name, p := range byName {
		seenKeepID[name] = p.ID
	}
	for _, p := range existing {
		keepID := seenKeepID[p.Name]
		isDup := keepID != 0 && p.ID != keepID
		_, wanted := desiredNames[p.Name]
		if wanted && !isDup {
			continue
		}
		if p.AccountCount > 0 {
			res.Skipped++
			continue
		}
		if err := s.Admin.DeleteProxy(ctx, p.ID); err != nil {
			return res, fmt.Errorf("delete proxy %s(%d): %w", p.Name, p.ID, err)
		}
		res.Deleted++
	}

	return res, nil
}

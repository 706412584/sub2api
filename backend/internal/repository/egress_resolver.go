package repository

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// EgressProxyResolver maps a target proxy URL to its configured egress (upstream)
// proxy URL. When a proxy has an egress proxy set, requests to the target proxy
// are first tunneled through the egress proxy.
//
// Lookups are cached per target-proxy-key with a short TTL so the hot request
// path does not hit the database on every call.
type EgressProxyResolver interface {
	// ResolveEgress returns the egress proxy URL for the given target proxy URL,
	// or "" when the target has no egress configured / is unknown.
	ResolveEgress(ctx context.Context, targetProxyURL string) string
}

// noOpEgressResolver is the default when no proxy repo is wired (e.g. tests).
type noOpEgressResolver struct{}

func (noOpEgressResolver) ResolveEgress(_ context.Context, _ string) string { return "" }

// cachedEgressResolver resolves egress proxy URLs from the proxy repository,
// caching results keyed by the normalized target proxy URL.
type cachedEgressResolver struct {
	proxyRepo service.ProxyRepository
	ttl       time.Duration

	mu    sync.RWMutex
	cache map[string]egressCacheEntry
}

type egressCacheEntry struct {
	egressURL string
	expiresAt time.Time
}

// NewEgressProxyResolver builds a resolver backed by the proxy repository.
func NewEgressProxyResolver(proxyRepo service.ProxyRepository) EgressProxyResolver {
	if proxyRepo == nil {
		return noOpEgressResolver{}
	}
	return &cachedEgressResolver{
		proxyRepo: proxyRepo,
		ttl:       30 * time.Second,
		cache:     make(map[string]egressCacheEntry),
	}
}

func (r *cachedEgressResolver) ResolveEgress(ctx context.Context, targetProxyURL string) string {
	if r == nil || r.proxyRepo == nil {
		return ""
	}
	key := strings.TrimSpace(targetProxyURL)
	if key == "" {
		return ""
	}

	// Fast path: read cached entry.
	r.mu.RLock()
	if entry, ok := r.cache[key]; ok && time.Now().Before(entry.expiresAt) {
		r.mu.RUnlock()
		return entry.egressURL
	}
	r.mu.RUnlock()

	egressURL := r.resolveFromRepo(ctx, key)

	r.mu.Lock()
	r.cache[key] = egressCacheEntry{
		egressURL: egressURL,
		expiresAt: time.Now().Add(r.ttl),
	}
	r.mu.Unlock()
	return egressURL
}

// resolveFromRepo walks the active proxy table for a proxy whose URL matches the
// target, then resolves its egress chain. The service layer limits the chain
// depth at write time; this guard also protects the request path from old or
// externally modified cyclic data.
func (r *cachedEgressResolver) resolveFromRepo(ctx context.Context, targetKey string) string {
	proxies, err := r.proxyRepo.ListActive(ctx)
	if err != nil || len(proxies) == 0 {
		return ""
	}
	byID := make(map[int64]service.Proxy, len(proxies))
	byURL := make(map[string]service.Proxy, len(proxies))
	for i := range proxies {
		p := proxies[i]
		byID[p.ID] = p
		key := p.URL()
		current, exists := byURL[key]
		// Prefer a configured egress over another duplicate active record with
		// the same endpoint, which is common for dynamic-pool snapshots.
		if !exists || (current.EgressProxyID == nil && p.EgressProxyID != nil) {
			byURL[key] = p
		}
	}

	current, ok := byURL[targetKey]
	if !ok || current.EgressProxyID == nil {
		return ""
	}
	visited := map[int64]struct{}{current.ID: {}}
	for depth := 0; depth < 3 && current.EgressProxyID != nil; depth++ {
		egress, ok := byID[*current.EgressProxyID]
		if !ok || !egress.IsActive() {
			return ""
		}
		if _, seen := visited[egress.ID]; seen {
			return ""
		}
		visited[egress.ID] = struct{}{}
		if egress.EgressProxyID == nil {
			return egress.URL()
		}
		current = egress
	}
	return ""
}

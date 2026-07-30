package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/subscription-sidecar/internal/adminapi"
	"github.com/Wei-Shaw/sub2api/subscription-sidecar/internal/config"
)

type fakeAdmin struct {
	proxies []adminapi.Proxy
	nextID  int64
	creates int
	updates int
	deletes int
}

func (f *fakeAdmin) ListOwnedProxies(ctx context.Context, namePrefix string) ([]adminapi.Proxy, error) {
	out := make([]adminapi.Proxy, 0)
	for _, p := range f.proxies {
		if strings.HasPrefix(p.Name, namePrefix) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeAdmin) CreateProxy(ctx context.Context, req adminapi.CreateProxyRequest) (*adminapi.Proxy, error) {
	f.nextID++
	p := adminapi.Proxy{
		ID:       f.nextID,
		Name:     req.Name,
		Protocol: req.Protocol,
		Host:     req.Host,
		Port:     req.Port,
		Status:   "active",
	}
	f.proxies = append(f.proxies, p)
	f.creates++
	return &p, nil
}

func (f *fakeAdmin) UpdateProxy(ctx context.Context, id int64, req adminapi.UpdateProxyRequest) (*adminapi.Proxy, error) {
	for i := range f.proxies {
		if f.proxies[i].ID == id {
			if req.Host != "" {
				f.proxies[i].Host = req.Host
			}
			if req.Port != 0 {
				f.proxies[i].Port = req.Port
			}
			if req.Protocol != "" {
				f.proxies[i].Protocol = req.Protocol
			}
			if req.Status != "" {
				f.proxies[i].Status = req.Status
			}
			if req.Name != "" {
				f.proxies[i].Name = req.Name
			}
			f.updates++
			cp := f.proxies[i]
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (f *fakeAdmin) DeleteProxy(ctx context.Context, id int64) error {
	for i := range f.proxies {
		if f.proxies[i].ID == id {
			if f.proxies[i].AccountCount > 0 {
				return fmt.Errorf("in use")
			}
			f.proxies = append(f.proxies[:i], f.proxies[i+1:]...)
			f.deletes++
			return nil
		}
	}
	return fmt.Errorf("not found")
}

func TestRunOnceCreateUpdatePrune(t *testing.T) {
	dir := t.TempDir()
	subPath := filepath.Join(dir, "sub.yaml")
	mihomoPath := filepath.Join(dir, "mihomo.yaml")
	fixture := `
proxies:
  - name: "Node-One"
    type: ss
    server: 1.1.1.1
    port: 443
    cipher: aes-128-gcm
    password: p
  - name: "Node-Two"
    type: trojan
    server: 2.2.2.2
    port: 443
    password: p
  - name: "Node-Three"
    type: vmess
    server: 3.3.3.3
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
`
	if err := os.WriteFile(subPath, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		SubscriptionFile: subPath,
		NamePrefix:       "sidecar-a-",
		Protocol:         "socks5",
		BindAddress:      "127.0.0.1",
		BasePort:         21080,
		MaxPorts:         2,
		MihomoConfigPath: mihomoPath,
	}
	admin := &fakeAdmin{nextID: 10}
	admin.proxies = append(admin.proxies, adminapi.Proxy{
		ID: 1, Name: "sidecar-a-old-stale", Protocol: "socks5", Host: "127.0.0.1", Port: 21099, Status: "active",
	})
	admin.proxies = append(admin.proxies, adminapi.Proxy{
		ID: 2, Name: "sidecar-a-old-inuse", Protocol: "socks5", Host: "127.0.0.1", Port: 21100, Status: "active", AccountCount: 3,
	})
	// inactive owned should be reactivated via update, not recreated
	// (will not match desired names; just ensure list includes inactive)

	svc := &Service{Cfg: cfg, Fetcher: FileFetcher{Path: subPath}, Admin: admin}
	res, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Desired) != 2 {
		t.Fatalf("desired=%d", len(res.Desired))
	}
	if res.ConfigHash == "" {
		t.Fatal("missing config hash")
	}
	if res.Created != 2 {
		t.Fatalf("created=%d", res.Created)
	}
	if res.Deleted != 1 {
		t.Fatalf("deleted=%d want 1 (stale free only)", res.Deleted)
	}
	if res.Skipped != 1 {
		t.Fatalf("skipped=%d want 1 in-use", res.Skipped)
	}
	if _, err := os.Stat(mihomoPath); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(mihomoPath)
	if !strings.Contains(string(raw), "listeners:") {
		t.Fatalf("mihomo config missing listeners: %s", raw)
	}

	res2, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res2.Created != 0 || res2.Unchanged != 2 {
		t.Fatalf("second run created=%d unchanged=%d updated=%d", res2.Created, res2.Unchanged, res2.Updated)
	}
	if res2.ConfigHash != res.ConfigHash {
		t.Fatalf("hash changed without input change")
	}
}

func TestRunOnceReactivatesInactive(t *testing.T) {
	dir := t.TempDir()
	subPath := filepath.Join(dir, "sub.yaml")
	_ = os.WriteFile(subPath, []byte(`
proxies:
  - name: "Only"
    type: http
    server: 9.9.9.9
    port: 80
`), 0o644)
	cfg := &config.Config{
		SubscriptionFile: subPath,
		NamePrefix:       "sidecar-a-",
		Protocol:         "http",
		BindAddress:      "127.0.0.1",
		BasePort:         30000,
		MaxPorts:         1,
		MihomoConfigPath: filepath.Join(dir, "m.yaml"),
	}
	// Precompute expected name by running once dry against empty admin after we know algorithm —
	// instead: first create active, mark inactive, re-run.
	admin := &fakeAdmin{nextID: 0}
	svc := &Service{Cfg: cfg, Fetcher: FileFetcher{Path: subPath}, Admin: admin}
	res, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 {
		t.Fatalf("created=%d", res.Created)
	}
	admin.proxies[0].Status = "inactive"
	res2, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res2.Updated != 1 || res2.Created != 0 {
		t.Fatalf("want reactivate update, got %+v admin=%+v", res2, admin.proxies)
	}
	if admin.proxies[0].Status != "active" {
		t.Fatalf("status=%s", admin.proxies[0].Status)
	}
}

func TestRunOnceDryRun(t *testing.T) {
	dir := t.TempDir()
	subPath := filepath.Join(dir, "sub.yaml")
	_ = os.WriteFile(subPath, []byte(`
proxies:
  - name: "Only"
    type: http
    server: 9.9.9.9
    port: 80
`), 0o644)
	cfg := &config.Config{
		SubscriptionFile: subPath,
		NamePrefix:       "sidecar-a-",
		Protocol:         "http",
		BindAddress:      "127.0.0.1",
		BasePort:         30000,
		MaxPorts:         1,
		MihomoConfigPath: filepath.Join(dir, "m.yaml"),
		DryRun:           true,
	}
	svc := &Service{Cfg: cfg, Fetcher: FileFetcher{Path: subPath}, Admin: nil}
	res, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || res.Created != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestRunOnceAllowFilterFailClosed(t *testing.T) {
	dir := t.TempDir()
	subPath := filepath.Join(dir, "sub.yaml")
	_ = os.WriteFile(subPath, []byte(`
proxies:
  - name: "US-Only"
    type: http
    server: 9.9.9.9
    port: 80
`), 0o644)
	cfg := &config.Config{
		SubscriptionFile: subPath,
		NamePrefix:       "sidecar-a-",
		Protocol:         "http",
		BindAddress:      "127.0.0.1",
		BasePort:         30000,
		MaxPorts:         5,
		MihomoConfigPath: filepath.Join(dir, "m.yaml"),
		DryRun:           true,
	}
	svc := &Service{Cfg: cfg, Fetcher: FileFetcher{Path: subPath}, Admin: nil, AllowContains: []string{"JP"}}
	if _, err := svc.RunOnce(context.Background()); err == nil {
		t.Fatal("expected allow filter error")
	}
}

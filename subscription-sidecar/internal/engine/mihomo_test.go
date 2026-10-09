package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/subscription-sidecar/internal/clash"
)

func TestBuildBindingsAndWriteConfig(t *testing.T) {
	nodes := []clash.Node{
		{Name: "N1", Type: "ss", Identity: "ss|a|1|N1", Raw: map[string]any{"name": "N1", "type": "ss", "server": "1.1.1.1", "port": 443}},
		{Name: "N2", Type: "trojan", Identity: "trojan|b|2|N2", Raw: map[string]any{"name": "N2", "type": "trojan", "server": "2.2.2.2", "port": 443}},
		{Name: "N3", Type: "vmess", Identity: "vmess|c|3|N3", Raw: map[string]any{"name": "N3", "type": "vmess", "server": "3.3.3.3", "port": 443}},
	}
	bindings, err := BuildBindings(nodes, "sidecar-a-", "127.0.0.1", "socks5", 21080, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 {
		t.Fatalf("got %d", len(bindings))
	}
	if bindings[0].Port != 21080 || bindings[1].Port != 21081 {
		t.Fatalf("ports %+v", bindings)
	}
	// names must not include -pPORT
	for _, b := range bindings {
		if strings.Contains(b.Name, "-p2108") {
			t.Fatalf("name still has port suffix: %s", b.Name)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	hash, err := WriteMihomoConfig(path, "127.0.0.1", "sidecar-a-", bindings)
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != 64 {
		t.Fatalf("hash %q", hash)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, need := range []string{"listeners:", "proxies:", "bind-address: 127.0.0.1", "type: mixed"} {
		if !strings.Contains(s, need) {
			t.Fatalf("missing %q in %s", need, s)
		}
	}
	hash2, err := WriteMihomoConfig(path, "127.0.0.1", "sidecar-a-", bindings)
	if err != nil || hash2 != hash {
		t.Fatalf("hash unstable %s vs %s err=%v", hash, hash2, err)
	}
}

func TestBuildBindingsAllowFailClosed(t *testing.T) {
	nodes := []clash.Node{
		{Name: "US-1", Type: "ss", Identity: "ss|a|1|US-1", Raw: map[string]any{"name": "US-1", "type": "ss"}},
	}
	_, err := BuildBindings(nodes, "sidecar-a-", "127.0.0.1", "socks5", 21080, 5, []string{"JP"})
	if err == nil {
		t.Fatal("expected allow filter error")
	}
}

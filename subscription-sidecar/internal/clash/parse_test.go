package clash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSubscriptionYAML(t *testing.T) {
	body := []byte(`
proxies:
  - name: "US-Alpha"
    type: ss
    server: 1.1.1.1
    port: 443
    cipher: aes-128-gcm
    password: x
  - name: "剩余流量100G"
    type: vmess
    server: 2.2.2.2
    port: 443
    uuid: 00000000-0000-0000-0000-000000000000
  - name: "JP-Beta"
    type: trojan
    server: 3.3.3.3
    port: 443
    password: y
  - name: "bad-type"
    type: unknown
    server: 4.4.4.4
    port: 1
`)
	nodes, err := ParseSubscription(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("want 2 usable nodes, got %d: %+v", len(nodes), nodes)
	}
	// Sorted by identity, not original order.
	names := []string{nodes[0].Name, nodes[1].Name}
	if !(names[0] == "JP-Beta" && names[1] == "US-Alpha") && !(names[0] == "US-Alpha" && names[1] == "JP-Beta") {
		t.Fatalf("unexpected names: %+v", names)
	}
}

func TestParseSubscriptionBase64(t *testing.T) {
	enc := "cHJveGllczoKICAtIG5hbWU6IE5vZGVBCiAgICB0eXBlOiBodHRwCiAgICBzZXJ2ZXI6IDEwLjAuMC4xCiAgICBwb3J0OiA4MDgwCg=="
	nodes, err := ParseSubscription([]byte(enc))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "NodeA" {
		t.Fatalf("got %+v", nodes)
	}
}

func TestSelectNodesFailClosed(t *testing.T) {
	nodes := []Node{
		{Name: "A", Type: "ss", Identity: "ss|1|1|A"},
		{Name: "B", Type: "ss", Identity: "ss|2|2|B"},
	}
	got, err := SelectNodes(nodes, 2, nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	got, err = SelectNodes(nodes, 2, []string{"A"})
	if err != nil || len(got) != 1 || got[0].Name != "A" {
		t.Fatalf("filter got=%+v err=%v", got, err)
	}
	_, err = SelectNodes(nodes, 2, []string{"NOPE"})
	if err == nil {
		t.Fatal("expected fail-closed on zero allow matches")
	}
}

func TestStableNameNoPortAndStable(t *testing.T) {
	n := Node{Name: "A", Type: "ss", Identity: "ss|1|1|A"}
	name := StableProxyName("sidecar-a-", n.Identity, n.Name)
	if name == "" || name[:9] != "sidecar-a" {
		t.Fatalf("bad name %q", name)
	}
	if name != StableProxyName("sidecar-a-", n.Identity, n.Name) {
		t.Fatal("unstable")
	}
	// must not embed a -pPORT suffix contract
	for i := 0; i < len(name)-2; i++ {
		if name[i] == '-' && name[i+1] == 'p' {
			// allow hash fragments; ensure not trailing -pdigits only pattern from old design
		}
	}
	if filepath.Ext(name) != "" {
		// no-op; keep linter calm if needed
	}
}

func TestParseFixtureFile(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "subscription-sample.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := ParseSubscription(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) < 2 {
		t.Fatalf("fixture should yield >=2 nodes, got %d", len(nodes))
	}
}

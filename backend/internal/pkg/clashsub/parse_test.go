package clashsub

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	if (names[0] != "JP-Beta" || names[1] != "US-Alpha") && (names[0] != "US-Alpha" || names[1] != "JP-Beta") {
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
	got, err := SelectNodes(nodes, 2, nil, nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	got, err = SelectNodes(nodes, 2, []string{"A"}, nil)
	if err != nil || len(got) != 1 || got[0].Name != "A" {
		t.Fatalf("filter got=%+v err=%v", got, err)
	}
	_, err = SelectNodes(nodes, 2, []string{"NOPE"}, nil)
	if err == nil {
		t.Fatal("expected fail-closed on zero allow matches")
	}
}

func TestSelectNodesIdentityAllowlist(t *testing.T) {
	nodes := []Node{
		{Name: "A", Type: "ss", Identity: "ss|1|1|A"},
		{Name: "B", Type: "ss", Identity: "ss|2|2|B"},
		{Name: "C", Type: "ss", Identity: "ss|3|3|C"},
	}
	got, err := SelectNodes(nodes, 2, nil, []string{"ss|2|2|B", "ss|3|3|C"})
	if err != nil || len(got) != 2 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if got[0].Name != "B" || got[1].Name != "C" {
		t.Fatalf("order/names %+v", got)
	}
	// Case-insensitive identity match.
	got, err = SelectNodes(nodes, 5, nil, []string{"SS|1|1|A"})
	if err != nil || len(got) != 1 || got[0].Name != "A" {
		t.Fatalf("case got=%+v err=%v", got, err)
	}
	// Combined with name filter.
	got, err = SelectNodes(nodes, 5, []string{"B"}, []string{"ss|2|2|B", "ss|1|1|A"})
	if err != nil || len(got) != 1 || got[0].Name != "B" {
		t.Fatalf("combined got=%+v err=%v", got, err)
	}
	_, err = SelectNodes(nodes, 5, nil, []string{"ss|9|9|Z"})
	if err == nil {
		t.Fatal("expected fail-closed on identity miss")
	}
	// Empty allowlist keeps auto behavior.
	got, err = SelectNodes(nodes, 2, nil, []string{})
	if err != nil || len(got) != 2 {
		t.Fatalf("empty allowlist got=%+v err=%v", got, err)
	}
}

func TestStableNameNoPortAndStable(t *testing.T) {
	n := Node{Name: "A", Type: "ss", Identity: "ss|1|1|A"}
	name := StableProxyName("sidecar-a-", n.Identity, n.Name)
	if !strings.HasPrefix(name, "sidecar-a-") {
		t.Fatalf("bad name %q", name)
	}
	if name != StableProxyName("sidecar-a-", n.Identity, n.Name) {
		t.Fatal("unstable")
	}
	if matched, _ := regexp.MatchString(`-p\d+$`, name); matched {
		t.Fatalf("name still looks port-suffixed: %q", name)
	}
	rest := strings.TrimPrefix(name, "sidecar-a-")
	parts := strings.SplitN(rest, "-", 2)
	if len(parts) < 1 || len(parts[0]) != 8 {
		t.Fatalf("expected 8-char hash prefix in name %q", name)
	}
}

func TestParseFixtureFile(t *testing.T) {
	path := filepath.Join("testdata", "subscription-sample.yaml")
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

// TestSanitizeNameFragmentKeepsCJK 钉死中文/分隔符处理：机场节点名里的地区中文
// 必须保留（否则 IP 管理列表全是 "05BGPCTCUCM" 之类无法辨认的串），
// '|' 等分隔符折叠为 '-'，emoji 国旗剥离，限长按 rune 计。
func TestSanitizeNameFragmentKeepsCJK(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"中文+分隔符", "🇸🇬新加坡05|BGP|CTC|UCM", 24, "新加坡05-BGP-CTC-UCM"},
		{"纯英文不变", "US-1", 24, "US-1"},
		{"emoji 剥离保留中文", "🇺🇸美国05|流媒体|0.1x", 24, "美国05-流媒体-0-1x"},
		{"空格折叠", "新加坡 · 新加坡", 24, "新加坡-新加坡"},
		{"rune 限长", "新加坡新加坡新加坡新加坡", 6, "新加坡新加坡"},
		{"全 emoji 落空", "🇸🇬🇺🇸", 24, ""},
		{"前导分隔符不留悬空", "|||香港01", 24, "香港01"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeNameFragment(tc.in, tc.max); got != tc.want {
				t.Fatalf("sanitizeNameFragment(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

// TestStableProxyNameReadableWithCJK 端到端：代理名应含可读中文片段。
func TestStableProxyNameReadableWithCJK(t *testing.T) {
	name := StableProxyName("sidecar-a-", "hysteria2|1.2.3.4|60000|SG05", "🇸🇬新加坡05|BGP|CTC|UCM")
	if !strings.Contains(name, "新加坡05") {
		t.Fatalf("proxy name should keep CJK region label, got %q", name)
	}
	if !strings.HasPrefix(name, "sidecar-a-") {
		t.Fatalf("bad prefix: %q", name)
	}
}

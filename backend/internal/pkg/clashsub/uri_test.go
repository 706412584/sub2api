package clashsub

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseVLESSShareList(t *testing.T) {
	body := strings.Join([]string{
		"vless://11111111-1111-1111-1111-111111111111@example.com:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=cloudflare.com&fp=chrome&pbk=PUBLIC&sid=abcd&type=tcp#US-Test-1",
		"vless://22222222-2222-2222-2222-222222222222@example.org:8443?encryption=none&security=tls&sni=example.org&type=ws&path=%2Fws&host=example.org#JP-Test-2",
		"vless://33333333-3333-3333-3333-333333333333@example.net:443?encryption=none&type=tcp#剩余流量展示",
	}, "\n")

	nodes, err := ParseSubscription([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("want 2, got %d %+v", len(nodes), nodeNames(nodes))
	}
	for _, n := range nodes {
		if n.Type != "vless" {
			t.Fatalf("type=%s", n.Type)
		}
		if asString(n.Raw["server"]) == "" || asString(n.Raw["uuid"]) == "" {
			t.Fatalf("missing fields: %+v", n.Raw)
		}
	}
}

func TestParseVLESSBase64List(t *testing.T) {
	plain := "vless://aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa@node.example:19822?encryption=none&security=reality&type=tcp&sni=cloudflare.com#Node-A\n"
	enc := base64.StdEncoding.EncodeToString([]byte(plain))
	nodes, err := ParseSubscription([]byte(enc))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "Node-A" || nodes[0].Type != "vless" {
		t.Fatalf("got %+v", nodes)
	}
	if asString(nodes[0].Raw["server"]) != "node.example" {
		t.Fatalf("server=%v", nodes[0].Raw["server"])
	}
}

func nodeNames(nodes []Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Name)
	}
	return out
}

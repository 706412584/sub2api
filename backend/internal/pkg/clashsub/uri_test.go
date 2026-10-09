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

func TestParseHysteria2(t *testing.T) {
	body := "hysteria2://fa7c6b3d-5be0-430a-b5b1-5d27e782af76@108.181.24.53:50000/?insecure=false&sni=www.microsoft.com&pinSHA256=6ae61c8d9818403b95cd43fef734e3e3e33ce8ffd7a24bf219f96b0f6c2548a7&mport=50000-53000#%F0%9F%87%BA%F0%9F%87%B8%E7%BE%8E%E5%9B%BD%E6%B4%9B%E6%9D%89%E7%9F%B61%E5%8F%B7\n" +
		"hysteria2://fa7c6b3d-5be0-430a-b5b1-5d27e782af76@108.181.23.123:50000/?insecure=false&sni=www.microsoft.com&pinSHA256=e19c8374572fd00c66abd8cea8ac2a8b2819894a30884ce5d5850663ea7286cb&mport=50000-53000#%F0%9F%87%BA%F0%9F%87%B8%E7%BE%8E%E5%9B%BD%E6%B4%9B%E6%9D%89%E7%9F%B62%E5%8F%B7\n" +
		"vless://fa7c6b3d-5be0-430a-b5b1-5d27e782af76@104.18.46.46:443?type=ws&encryption=none&security=tls&sni=www.microsoft.com#%E5%89%A9%E4%BD%99%E6%B5%81%E9%87%8F%EF%BC%9A199.16%20GB"
	nodes, err := ParseSubscription([]byte(body))
	if err != nil {
		t.Fatalf("ParseSubscription: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 hysteria2 nodes, got %d: %v", len(nodes), nodeNames(nodes))
	}
	for _, n := range nodes {
		if n.Type != "hysteria2" {
			t.Errorf("expected hysteria2, got %q", n.Type)
		}
		server, _ := n.Raw["server"].(string)
		port, _ := n.Raw["port"].(int)
		if server == "" || port == 0 {
			t.Errorf("node %q missing server/port", n.Name)
		}
	}
}

func TestParseHysteria2Base64(t *testing.T) {
	plain := "hysteria2://fa7c6b3d-5be0-430a-b5b1-5d27e782af76@108.181.24.53:50000/?insecure=false&sni=www.microsoft.com&pinSHA256=6ae61c8d9818403b95cd43fef734e3e3e33ce8ffd7a24bf219f96b0f6c2548a7&mport=50000-53000#Node-Hy2\n"
	enc := base64.StdEncoding.EncodeToString([]byte(plain))
	nodes, err := ParseSubscription([]byte(enc))
	if err != nil {
		t.Fatalf("ParseSubscription base64: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Type != "hysteria2" {
		t.Errorf("expected hysteria2, got %q", nodes[0].Type)
	}
	if nodes[0].Name != "Node-Hy2" {
		t.Errorf("expected name Node-Hy2, got %q", nodes[0].Name)
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

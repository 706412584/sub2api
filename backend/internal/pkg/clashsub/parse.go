package clashsub

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// Node is one Clash outbound proxy entry we can expose locally.
type Node struct {
	Name     string
	Type     string
	Raw      map[string]any
	Identity string // stable identity for naming (type|server|port|name)
}

var defaultDenyName = regexp.MustCompile(`(?i)(过期|到期|剩余|流量|官网|机场|重置|expire|traffic|remain|deprecated|invalid)`)

// ParseSubscription decodes Clash YAML, base64 Clash YAML, or base64/plain share-link lists
// (vless:// vmess:// trojan:// ss://).
func ParseSubscription(body []byte) ([]Node, error) {
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return nil, fmt.Errorf("empty subscription body")
	}

	// 1) Share-link list (common airport export): plain or base64.
	if nodes, err := parseShareLinkSubscription(raw); err == nil && len(nodes) > 0 {
		return finalizeNodes(nodes)
	}

	// 2) Clash YAML (plain or base64-wrapped).
	yamlText := raw
	if !looksLikeClashYAML(raw) {
		decoded, err := decodeBase64Flexible(raw)
		if err != nil {
			// Fall through with original text for a clearer error below.
		} else {
			yamlText = strings.TrimSpace(string(decoded))
			if nodes, err := parseShareLinkSubscription(yamlText); err == nil && len(nodes) > 0 {
				return finalizeNodes(nodes)
			}
		}
	}

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		// Last attempt: share links on original raw for better diagnostics.
		if nodes, linkErr := parseShareLinkSubscription(raw); linkErr == nil && len(nodes) > 0 {
			return finalizeNodes(nodes)
		}
		return nil, fmt.Errorf("parse clash yaml: %w", err)
	}

	proxiesAny, ok := doc["proxies"]
	if !ok {
		if nodes, err := parseShareLinkSubscription(yamlText); err == nil && len(nodes) > 0 {
			return finalizeNodes(nodes)
		}
		return nil, fmt.Errorf("clash document missing proxies")
	}
	list, ok := proxiesAny.([]any)
	if !ok {
		return nil, fmt.Errorf("proxies is not a list")
	}

	out := make([]Node, 0, len(list))
	seen := map[string]struct{}{}
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(asString(m["name"]))
		typ := strings.ToLower(strings.TrimSpace(asString(m["type"])))
		if name == "" || typ == "" {
			continue
		}
		if defaultDenyName.MatchString(name) {
			continue
		}
		if !supportedType(typ) {
			continue
		}
		id := identityOf(name, typ, m)
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, Node{
			Name:     name,
			Type:     typ,
			Raw:      m,
			Identity: id,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no usable proxy nodes in subscription")
	}
	return finalizeNodes(out)
}

func finalizeNodes(out []Node) ([]Node, error) {
	if len(out) == 0 {
		return nil, fmt.Errorf("no usable proxy nodes in subscription")
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Identity == out[j].Identity {
			return out[i].Name < out[j].Name
		}
		return out[i].Identity < out[j].Identity
	})
	return out, nil
}

// SelectNodes picks up to max nodes.
// If allowContains is non-empty and nothing matches, returns error (fail-closed).
func SelectNodes(nodes []Node, max int, allowContains []string) ([]Node, error) {
	if max <= 0 {
		return nil, nil
	}
	filtered := nodes
	if len(allowContains) > 0 {
		tmp := make([]Node, 0, len(nodes))
		for _, n := range nodes {
			if nameMatchesAny(n.Name, allowContains) {
				tmp = append(tmp, n)
			}
		}
		if len(tmp) == 0 {
			return nil, fmt.Errorf("no nodes matched allow filter %v", allowContains)
		}
		filtered = tmp
	}
	if len(filtered) > max {
		return filtered[:max], nil
	}
	return filtered, nil
}

// StableProxyName builds Sub2API proxy name from identity only (no local port).
// Format: {prefix}{hash8}-{sanitizedFragment}
func StableProxyName(prefix, identity, nodeName string) string {
	hash := shortHash(identity)
	frag := sanitizeNameFragment(nodeName, 24)
	if frag == "" {
		frag = "node"
	}
	return fmt.Sprintf("%s%s-%s", prefix, hash, frag)
}

func looksLikeClashYAML(s string) bool {
	return strings.Contains(s, "proxies:") || strings.Contains(s, "proxy-groups:") || strings.HasPrefix(s, "port:") || strings.HasPrefix(s, "mixed-port:")
}

func decodeBase64Flexible(s string) ([]byte, error) {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	if decoded, err := base64.StdEncoding.DecodeString(clean); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.URLEncoding.DecodeString(clean); err == nil {
		return decoded, nil
	}
	if m := len(clean) % 4; m != 0 {
		clean += strings.Repeat("=", 4-m)
	}
	return base64.StdEncoding.DecodeString(clean)
}

func supportedType(t string) bool {
	switch t {
	case "ss", "ssr", "vmess", "vless", "trojan", "http", "socks5", "hysteria", "hysteria2", "tuic", "wireguard", "snell", "anytls":
		return true
	default:
		return false
	}
}

func identityOf(name, typ string, m map[string]any) string {
	server := asString(m["server"])
	if server == "" {
		server = asString(m["servername"])
	}
	port := asString(m["port"])
	return strings.ToLower(typ + "|" + server + "|" + port + "|" + name)
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int:
		return fmt.Sprintf("%d", t)
	case int64:
		return fmt.Sprintf("%d", t)
	case float64:
		if t == float64(int(t)) {
			return fmt.Sprintf("%d", int(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		if v == nil {
			return ""
		}
		return fmt.Sprintf("%v", v)
	}
}

func nameMatchesAny(name string, needles []string) bool {
	lower := strings.ToLower(name)
	for _, n := range needles {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" && strings.Contains(lower, n) {
			return true
		}
	}
	return false
}

func sanitizeNameFragment(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
		if b.Len() >= max {
			break
		}
	}
	return strings.Trim(b.String(), "-_")
}

func shortHash(s string) string {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return fmt.Sprintf("%08x", h)
}

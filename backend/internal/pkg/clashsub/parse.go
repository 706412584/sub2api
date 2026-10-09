package clashsub

import (
	"encoding/base64"
	"fmt"
	"hash/fnv"
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
// allowContains: optional name-substring filter (fail-closed when non-empty).
// identityAllow: optional exact Identity allowlist (fail-closed when non-empty).
// When both are set, name filter runs first, then identity filter.
func SelectNodes(nodes []Node, max int, allowContains, identityAllow []string) ([]Node, error) {
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
	if len(identityAllow) > 0 {
		want := make(map[string]struct{}, len(identityAllow))
		for _, id := range identityAllow {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			want[strings.ToLower(id)] = struct{}{}
		}
		if len(want) == 0 {
			return nil, fmt.Errorf("node identity allowlist is empty after normalize")
		}
		tmp := make([]Node, 0, len(filtered))
		for _, n := range filtered {
			if _, ok := want[strings.ToLower(n.Identity)]; ok {
				tmp = append(tmp, n)
			}
		}
		if len(tmp) == 0 {
			return nil, fmt.Errorf("no nodes matched identity allowlist")
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

// ProxyNameIdentityKey 取代理名里稳定的身份部分（{prefix}{hash8}）。
// hash8 只由节点 identity 决定，与可读片段无关，因此可读片段规则变化
// （例如开始保留中文）后仍能认出同一节点，避免同步时重建代理行、
// 丢掉已绑定的账号。不符合托管命名格式时返回空串。
func ProxyNameIdentityKey(prefix, name string) string {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok || len(rest) < 8 {
		return ""
	}
	hash := rest[:8]
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	return prefix + hash
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

// sanitizeNameFragment 从节点名提取可读片段用于代理名。
// 保留 ASCII 字母数字与 CJK 中文（机场节点名常见「新加坡05」这类地区标识，
// 剥掉后只剩 "05BGPCTCUCM" 之类无法辨认的串）；分隔符（|、空格、·、/）
// 统一折叠为单个 '-'；emoji 国旗等符号剥离（占长度且无辨识价值）。
// max 按 rune 计数而非字节，否则一个中文吃 3 字节，24 上限只能放 8 个汉字。
func sanitizeNameFragment(s string, max int) string {
	var runes []rune
	pendingSep := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			isCJKNameRune(r):
			if pendingSep && len(runes) > 0 {
				runes = append(runes, '-')
			}
			pendingSep = false
			runes = append(runes, r)
		case r == '-' || r == '_':
			if len(runes) > 0 {
				pendingSep = false
				runes = append(runes, r)
			}
		case r == '|' || r == '/' || r == '\\' || r == '·' || r == '.' || unicode.IsSpace(r):
			// 分隔符：延迟落笔，避免结尾出现悬空 '-'
			pendingSep = true
		}
		if len(runes) >= max {
			break
		}
	}
	return strings.Trim(string(runes), "-_")
}

// isCJKNameRune 报告该字符是否为节点名中值得保留的中日韩文字。
// 覆盖 CJK 统一表意文字（基本区 + 扩展 A）与假名、韩文音节，
// 不含 emoji / 区域指示符（国旗）等符号。
func isCJKNameRune(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // CJK 统一表意文字
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK 扩展 A
		return true
	case r >= 0x3040 && r <= 0x30FF: // 平假名 / 片假名
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // 韩文音节
		return true
	default:
		return false
	}
}

func shortHash(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

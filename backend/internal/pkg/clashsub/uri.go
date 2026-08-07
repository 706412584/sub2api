package clashsub

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// parseShareLinkSubscription parses base64-or-plain multi-line share links:
// vless:// vmess:// trojan:// ss:// hysteria2:// ...
func parseShareLinkSubscription(body string) ([]Node, error) {
	text := strings.TrimSpace(body)
	if text == "" {
		return nil, fmt.Errorf("empty share-link body")
	}

	// If the whole payload is base64 of link lines, decode first.
	if !looksLikeShareLinks(text) {
		decoded, err := decodeBase64Flexible(text)
		if err != nil {
			return nil, fmt.Errorf("not share-link list: %w", err)
		}
		text = strings.TrimSpace(string(decoded))
	}
	if !looksLikeShareLinks(text) {
		return nil, fmt.Errorf("body is not share-link subscription")
	}

	lines := strings.Split(text, "\n")
	out := make([]Node, 0, len(lines))
	seen := map[string]struct{}{}
	var firstErr error
	parsed := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		node, err := parseShareLink(line)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if defaultDenyName.MatchString(node.Name) {
			continue
		}
		if _, ok := seen[node.Identity]; ok {
			continue
		}
		seen[node.Identity] = struct{}{}
		out = append(out, node)
		parsed++
	}
	if len(out) == 0 {
		if firstErr != nil {
			return nil, fmt.Errorf("no usable share links: %w", firstErr)
		}
		return nil, fmt.Errorf("no usable share links")
	}
	return out, nil
}

func looksLikeShareLinks(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "vless://") ||
		strings.Contains(lower, "vmess://") ||
		strings.Contains(lower, "trojan://") ||
		strings.Contains(lower, "ss://") ||
		strings.Contains(lower, "hysteria2://") ||
		strings.Contains(lower, "hy2://") ||
		strings.Contains(lower, "ssr://")
}

func parseShareLink(raw string) (Node, error) {
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "vless://"):
		return parseVLESS(raw)
	case strings.HasPrefix(lower, "vmess://"):
		return parseVMESS(raw)
	case strings.HasPrefix(lower, "trojan://"):
		return parseTrojan(raw)
	case strings.HasPrefix(lower, "ss://"):
		return parseShadowsocks(raw)
	case strings.HasPrefix(lower, "hysteria2://"), strings.HasPrefix(lower, "hy2://"):
		return parseHysteria2(raw)
	default:
		scheme := strings.SplitN(raw, "://", 2)[0]
		return Node{}, fmt.Errorf("unsupported share scheme %q", scheme)
	}
}

// parseHysteria2 parses hysteria2://udp_password@host:port/?params#name
// and hy2:// aliases. Common params: insecure, sni, pinSHA256, obfs,
// obfs-password, up, down, alpn, mport.
func parseHysteria2(raw string) (Node, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Node{}, err
	}
	host := u.Hostname()
	port := u.Port()
	password := u.User.Username()
	if host == "" || port == "" || password == "" {
		return Node{}, fmt.Errorf("hysteria2 missing host/port/password")
	}
	q := u.Query()
	name := host + ":" + port
	if frag := strings.TrimSpace(u.Fragment); frag != "" {
		if unescaped, err := url.QueryUnescape(frag); err == nil {
			name = strings.TrimSpace(unescaped)
		}
	}
	portN, _ := strconv.Atoi(port)
	m := map[string]any{
		"name":          name,
		"type":          "hysteria2",
		"server":        host,
		"port":          portN,
		"password":      password,
		"obfs":          q.Get("obfs"),
		"obfs-password": q.Get("obfs-password"),
	}
	if sni := firstNonEmpty(q.Get("sni"), q.Get("peer"), q.Get("servername")); sni != "" {
		m["sni"] = sni
	}
	if insecure := q.Get("insecure"); insecure != "" {
		m["skip-cert-verify"] = insecure == "true" || insecure == "1"
	}
	if pin := q.Get("pinSHA256"); pin != "" {
		m["pinSHA256"] = pin
	}
	if up := q.Get("up"); up != "" {
		m["up"] = up
	}
	if down := q.Get("down"); down != "" {
		m["down"] = down
	}
	if alpn := q.Get("alpn"); alpn != "" {
		m["alpn"] = splitCSV(alpn)
	}
	id := identityOf(name, "hysteria2", m)
	return Node{Name: name, Type: "hysteria2", Raw: m, Identity: id}, nil
}

func parseVLESS(raw string) (Node, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Node{}, err
	}
	host := u.Hostname()
	port := u.Port()
	if host == "" || port == "" {
		return Node{}, fmt.Errorf("vless missing host/port")
	}
	uuid := u.User.Username()
	if uuid == "" {
		return Node{}, fmt.Errorf("vless missing uuid")
	}
	q := u.Query()
	name := host + ":" + port
	if remarks := strings.TrimSpace(q.Get("remarks")); remarks != "" {
		name = remarks
	}
	if unescaped, err := url.QueryUnescape(u.Fragment); err == nil && strings.TrimSpace(unescaped) != "" {
		name = strings.TrimSpace(unescaped)
	}
	portN, _ := strconv.Atoi(port)
	network := strings.ToLower(firstNonEmpty(q.Get("type"), q.Get("network"), "tcp"))
	security := strings.ToLower(q.Get("security"))
	tls := security == "tls" || security == "reality"
	m := map[string]any{
		"name":    name,
		"type":    "vless",
		"server":  host,
		"port":    portN,
		"uuid":    uuid,
		"network": network,
		"udp":     true,
		"tls":     tls,
	}
	if enc := q.Get("encryption"); enc != "" {
		m["encryption"] = enc
	}
	if flow := q.Get("flow"); flow != "" {
		m["flow"] = flow
	}
	if sni := firstNonEmpty(q.Get("sni"), q.Get("servername")); sni != "" {
		m["servername"] = sni
	}
	if fp := q.Get("fp"); fp != "" {
		m["client-fingerprint"] = fp
	}
	if alpn := q.Get("alpn"); alpn != "" {
		m["alpn"] = splitCSV(alpn)
	}
	if security == "reality" {
		opts := map[string]any{}
		if pbk := q.Get("pbk"); pbk != "" {
			opts["public-key"] = pbk
		}
		if sid := q.Get("sid"); sid != "" {
			opts["short-id"] = sid
		}
		if spx := q.Get("spx"); spx != "" {
			opts["spider-x"] = spx
		}
		m["reality-opts"] = opts
	}
	switch network {
	case "ws":
		ws := map[string]any{}
		if path := q.Get("path"); path != "" {
			ws["path"] = path
		}
		if h := firstNonEmpty(q.Get("host"), q.Get("Host")); h != "" {
			ws["headers"] = map[string]any{"Host": h}
		}
		m["ws-opts"] = ws
	case "grpc":
		grpc := map[string]any{}
		if sn := firstNonEmpty(q.Get("serviceName"), q.Get("servicename")); sn != "" {
			grpc["grpc-service-name"] = sn
		}
		m["grpc-opts"] = grpc
	case "tcp":
		// optional header type
	}
	id := identityOf(name, "vless", m)
	return Node{Name: name, Type: "vless", Raw: m, Identity: id}, nil
}

func parseTrojan(raw string) (Node, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Node{}, err
	}
	host := u.Hostname()
	port := u.Port()
	password, _ := u.User.Password()
	if password == "" {
		password = u.User.Username()
	}
	if host == "" || port == "" || password == "" {
		return Node{}, fmt.Errorf("trojan missing host/port/password")
	}
	q := u.Query()
	name := host + ":" + port
	if frag := strings.TrimSpace(u.Fragment); frag != "" {
		if unescaped, err := url.QueryUnescape(frag); err == nil {
			name = strings.TrimSpace(unescaped)
		}
	}
	portN, _ := strconv.Atoi(port)
	m := map[string]any{
		"name":     name,
		"type":     "trojan",
		"server":   host,
		"port":     portN,
		"password": password,
		"udp":      true,
	}
	if sni := firstNonEmpty(q.Get("sni"), q.Get("peer")); sni != "" {
		m["sni"] = sni
	}
	if network := strings.ToLower(q.Get("type")); network != "" && network != "tcp" {
		m["network"] = network
	}
	id := identityOf(name, "trojan", m)
	return Node{Name: name, Type: "trojan", Raw: m, Identity: id}, nil
}

func parseShadowsocks(raw string) (Node, error) {
	// ss://BASE64(method:pass@host:port)#name  OR ss://method:pass@host:port
	u, err := url.Parse(raw)
	if err != nil {
		return Node{}, err
	}
	var method, password, host, port, name string
	name = strings.TrimSpace(u.Fragment)
	if unescaped, err := url.QueryUnescape(name); err == nil {
		name = strings.TrimSpace(unescaped)
	}

	if u.User != nil && u.Hostname() != "" {
		// decoded form method:pass@host:port maybe already in userinfo
		method = u.User.Username()
		password, _ = u.User.Password()
		host = u.Hostname()
		port = u.Port()
		// sometimes user is base64(method:pass)
		if password == "" && method != "" && !strings.Contains(method, ":") {
			if dec, err := decodeBase64Flexible(method); err == nil {
				mp := strings.SplitN(string(dec), ":", 2)
				if len(mp) == 2 {
					method, password = mp[0], mp[1]
				}
			}
		}
	} else {
		// ss://base64...
		rest := strings.TrimPrefix(raw, "ss://")
		rest = strings.TrimPrefix(rest, "SS://")
		if i := strings.Index(rest, "#"); i >= 0 {
			if name == "" {
				if unescaped, err := url.QueryUnescape(rest[i+1:]); err == nil {
					name = strings.TrimSpace(unescaped)
				}
			}
			rest = rest[:i]
		}
		if i := strings.Index(rest, "?"); i >= 0 {
			rest = rest[:i]
		}
		dec, err := decodeBase64Flexible(rest)
		if err != nil {
			return Node{}, fmt.Errorf("ss decode: %w", err)
		}
		// method:password@host:port
		s := string(dec)
		at := strings.LastIndex(s, "@")
		if at < 0 {
			return Node{}, fmt.Errorf("ss invalid payload")
		}
		mp := strings.SplitN(s[:at], ":", 2)
		if len(mp) != 2 {
			return Node{}, fmt.Errorf("ss missing method/password")
		}
		method, password = mp[0], mp[1]
		hp := s[at+1:]
		host, port, err = splitHostPortFlexible(hp)
		if err != nil {
			return Node{}, err
		}
	}
	if host == "" || port == "" || method == "" || password == "" {
		return Node{}, fmt.Errorf("ss incomplete")
	}
	if name == "" {
		name = host + ":" + port
	}
	portN, _ := strconv.Atoi(port)
	m := map[string]any{
		"name":     name,
		"type":     "ss",
		"server":   host,
		"port":     portN,
		"cipher":   method,
		"password": password,
		"udp":      true,
	}
	id := identityOf(name, "ss", m)
	return Node{Name: name, Type: "ss", Raw: m, Identity: id}, nil
}

func parseVMESS(raw string) (Node, error) {
	// vmess://base64(json)
	rest := strings.TrimPrefix(raw, "vmess://")
	rest = strings.TrimPrefix(rest, "VMESS://")
	if i := strings.Index(rest, "#"); i >= 0 {
		rest = rest[:i]
	}
	dec, err := decodeBase64Flexible(rest)
	if err != nil {
		return Node{}, fmt.Errorf("vmess decode: %w", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(dec, &obj); err != nil {
		return Node{}, fmt.Errorf("vmess json: %w", err)
	}
	name := strings.TrimSpace(asString(obj["ps"]))
	host := strings.TrimSpace(asString(obj["add"]))
	port := strings.TrimSpace(asString(obj["port"]))
	uuid := strings.TrimSpace(asString(obj["id"]))
	if name == "" {
		name = host + ":" + port
	}
	if host == "" || port == "" || uuid == "" {
		return Node{}, fmt.Errorf("vmess incomplete")
	}
	portN, _ := strconv.Atoi(port)
	aid := 0
	if v := strings.TrimSpace(asString(obj["aid"])); v != "" {
		aid, _ = strconv.Atoi(v)
	}
	network := strings.ToLower(firstNonEmpty(asString(obj["net"]), "tcp"))
	tls := strings.EqualFold(asString(obj["tls"]), "tls")
	m := map[string]any{
		"name":    name,
		"type":    "vmess",
		"server":  host,
		"port":    portN,
		"uuid":    uuid,
		"alterId": aid,
		"cipher":  firstNonEmpty(asString(obj["scy"]), "auto"),
		"network": network,
		"udp":     true,
		"tls":     tls,
	}
	if sni := firstNonEmpty(asString(obj["sni"]), asString(obj["host"])); sni != "" && tls {
		m["servername"] = sni
	}
	if network == "ws" {
		ws := map[string]any{}
		if path := asString(obj["path"]); path != "" {
			ws["path"] = path
		}
		if h := asString(obj["host"]); h != "" {
			ws["headers"] = map[string]any{"Host": h}
		}
		m["ws-opts"] = ws
	}
	id := identityOf(name, "vmess", m)
	return Node{Name: name, Type: "vmess", Raw: m, Identity: id}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitHostPortFlexible(hp string) (string, string, error) {
	if strings.HasPrefix(hp, "[") {
		// [ipv6]:port
		end := strings.Index(hp, "]")
		if end < 0 {
			return "", "", fmt.Errorf("bad ipv6 host")
		}
		host := hp[1:end]
		rest := hp[end+1:]
		if !strings.HasPrefix(rest, ":") {
			return "", "", fmt.Errorf("missing port")
		}
		return host, rest[1:], nil
	}
	i := strings.LastIndex(hp, ":")
	if i < 0 {
		return "", "", fmt.Errorf("missing port")
	}
	return hp[:i], hp[i+1:], nil
}

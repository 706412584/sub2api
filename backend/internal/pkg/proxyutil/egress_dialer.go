package proxyutil

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
)

// egressDialer wraps a base dialer so that TCP connections to the target proxy
// are first tunneled through an egress (upstream) proxy. The full request path
// becomes: sub2api → egress proxy → target proxy → destination.
//
// The target proxy itself is still applied by http.Transport (via
// ConfigureTransportProxy) on top of this dialer, so only the TCP hop to the
// target proxy is chained.
type EgressDialer struct {
	baseDialer     *net.Dialer
	egressProxyURL *url.URL
	egressAddr     string
}

// NewEgressDialer builds a dialer that tunnels through the egress proxy.
// Supported egress proxy schemes: http, https, socks5, socks5h.
func NewEgressDialer(egressProxyURL string, base *net.Dialer) (*EgressDialer, error) {
	if strings.TrimSpace(egressProxyURL) == "" {
		return nil, fmt.Errorf("egress proxy url is empty")
	}
	parsed, err := url.Parse(strings.TrimSpace(egressProxyURL))
	if err != nil {
		return nil, fmt.Errorf("parse egress proxy url: %w", err)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("egress proxy missing host: %s", egressProxyURL)
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if host == "" || port == "" {
		return nil, fmt.Errorf("egress proxy missing host/port: %s", parsed.String())
	}
	return &EgressDialer{
		baseDialer:     base,
		egressProxyURL: parsed,
		egressAddr:     net.JoinHostPort(host, port),
	}, nil
}

// DialContext connects to addr (the target proxy's host:port) by first dialing
// the egress proxy and establishing a tunnel to addr through it.
func (d *EgressDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if d == nil || d.baseDialer == nil {
		return nil, fmt.Errorf("egress dialer not initialized")
	}
	conn, err := d.baseDialer.DialContext(ctx, "tcp", d.egressAddr)
	if err != nil {
		return nil, fmt.Errorf("dial egress proxy %s: %w", d.egressAddr, err)
	}

	scheme := strings.ToLower(d.egressProxyURL.Scheme)
	switch scheme {
	case "http":
		if err := d.connectThroughProxy(ctx, conn, addr); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	case "https":
		tlsConn := tls.Client(conn, &tls.Config{ServerName: d.egressProxyURL.Hostname(), MinVersion: tls.VersionTLS12})
		SetDeadline(ctx, tlsConn, 30*time.Second)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("egress proxy TLS handshake: %w", err)
		}
		_ = tlsConn.SetDeadline(time.Time{})
		if err := d.connectThroughProxy(ctx, tlsConn, addr); err != nil {
			_ = tlsConn.Close()
			return nil, err
		}
		return tlsConn, nil
	case "socks5", "socks5h":
		if err := d.socks5Tunnel(ctx, conn, addr); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	default:
		_ = conn.Close()
		return nil, fmt.Errorf("unsupported egress proxy scheme: %s", scheme)
	}
}

// connectThroughProxy sends an HTTP CONNECT to the egress proxy asking it to
// tunnel to addr, then reads the proxy's response.
func (d *EgressDialer) connectThroughProxy(ctx context.Context, conn net.Conn, addr string) error {
	connectLine := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", addr, addr)
	if d.egressProxyURL.User != nil {
		creds := d.egressProxyURL.User.String()
		connectLine += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(creds)) + "\r\n"
	}
	connectLine += "\r\n"

	SetDeadline(ctx, conn, 30*time.Second)
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	if _, err := conn.Write([]byte(connectLine)); err != nil {
		return fmt.Errorf("write CONNECT to egress: %w", err)
	}

	buf := make([]byte, 0, 256)
	tmp := make([]byte, 1)
	for {
		n, err := conn.Read(tmp)
		if err != nil {
			return fmt.Errorf("read CONNECT response: %w", err)
		}
		buf = append(buf, tmp[:n]...)
		if strings.Contains(string(buf), "\r\n\r\n") || len(buf) > 8192 {
			break
		}
	}
	resp := string(buf)
	if !strings.Contains(resp, " 200 ") {
		firstLine := strings.SplitN(resp, "\r\n", 2)[0]
		return fmt.Errorf("egress CONNECT rejected: %s", firstLine)
	}
	return nil
}

// socks5Tunnel performs a minimal SOCKS5 CONNECT to addr through the egress
// proxy connection.
func (d *EgressDialer) socks5Tunnel(ctx context.Context, conn net.Conn, addr string) error {
	SetDeadline(ctx, conn, 30*time.Second)
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	greeting := []byte{0x05, 0x01, 0x00}
	if d.egressProxyURL.User != nil {
		greeting = []byte{0x05, 0x02, 0x00, 0x02}
	}
	if _, err := conn.Write(greeting); err != nil {
		return fmt.Errorf("socks5 greeting: %w", err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return fmt.Errorf("socks5 greeting response: %w", err)
	}
	if resp[0] != 0x05 {
		return fmt.Errorf("socks5 invalid version: %d", resp[0])
	}
	if resp[1] == 0xFF {
		return fmt.Errorf("socks5 egress proxy refused auth methods")
	}
	if resp[1] == 0x02 {
		user := d.egressProxyURL.User.Username()
		pass, _ := d.egressProxyURL.User.Password()
		authMsg := []byte{0x01, byte(len(user))}
		authMsg = append(authMsg, []byte(user)...)
		authMsg = append(authMsg, byte(len(pass)))
		authMsg = append(authMsg, []byte(pass)...)
		if _, err := conn.Write(authMsg); err != nil {
			return fmt.Errorf("socks5 auth write: %w", err)
		}
		authResp := make([]byte, 2)
		if _, err := io.ReadFull(conn, authResp); err != nil {
			return fmt.Errorf("socks5 auth response: %w", err)
		}
		if authResp[1] != 0x00 {
			return fmt.Errorf("socks5 auth failed")
		}
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("split addr %s: %w", addr, err)
	}
	var portNum int
	if _, err := fmt.Sscanf(port, "%d", &portNum); err != nil {
		return fmt.Errorf("parse port %s: %w", port, err)
	}
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, []byte(host)...)
	req = append(req, byte(portNum>>8), byte(portNum&0xFF))
	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("socks5 connect write: %w", err)
	}
	rep := make([]byte, 10)
	if _, err := io.ReadFull(conn, rep); err != nil {
		return fmt.Errorf("socks5 connect response: %w", err)
	}
	if rep[1] != 0x00 {
		return fmt.Errorf("socks5 connect failed: code %d", rep[1])
	}
	return nil
}

func SetDeadline(ctx context.Context, conn net.Conn, fallback time.Duration) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(fallback))
	}
}

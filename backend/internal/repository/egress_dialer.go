package repository

import (
	"context"
	"net"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
)

// egressDialer 保持原 repository 内部类型，实现委托给
// proxyutil.NewEgressDialer（见 pkg/proxyutil/egress_dialer.go，
// 2026-09 从本文件迁出，供 antigravity OAuth 刷新等 service 路径复用）。
type egressDialer struct {
	inner *proxyutil.EgressDialer
}

func newEgressDialer(egressProxyURL string, base *net.Dialer) (*egressDialer, error) {
	inner, err := proxyutil.NewEgressDialer(egressProxyURL, base)
	if err != nil {
		return nil, err
	}
	return &egressDialer{inner: inner}, nil
}

func (d *egressDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.inner.DialContext(ctx, network, addr)
}

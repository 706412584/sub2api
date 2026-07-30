package config

import (
	"testing"
)

func TestLoadRequiresSubscriptionSource(t *testing.T) {
	t.Setenv("SIDECAR_SUBSCRIPTION_URL", "")
	t.Setenv("SIDECAR_SUBSCRIPTION_FILE", "")
	t.Setenv("SIDECAR_ADMIN_API_KEY", "admin-test")
	t.Setenv("SIDECAR_DRY_RUN", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error without subscription source")
	}
}

func TestLoadDryRunWithoutAdminKey(t *testing.T) {
	t.Setenv("SIDECAR_SUBSCRIPTION_FILE", "testdata/x.yaml")
	t.Setenv("SIDECAR_SUBSCRIPTION_URL", "")
	t.Setenv("SIDECAR_ADMIN_API_KEY", "")
	t.Setenv("SIDECAR_DRY_RUN", "1")
	t.Setenv("SIDECAR_BIND_ADDRESS", "127.0.0.1")
	t.Setenv("SIDECAR_ALLOW_NON_LOCAL_BIND", "")
	t.Setenv("SIDECAR_NAME_PREFIX", "sidecar-a-")
	t.Setenv("SIDECAR_PROTOCOL", "socks5")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DryRun {
		t.Fatal("expected dry run")
	}
	if cfg.BasePort != DefaultBasePort {
		t.Fatalf("base port %d", cfg.BasePort)
	}
}

func TestLoadRejectsBadPrefix(t *testing.T) {
	t.Setenv("SIDECAR_SUBSCRIPTION_FILE", "x.yaml")
	t.Setenv("SIDECAR_DRY_RUN", "1")
	t.Setenv("SIDECAR_NAME_PREFIX", "proxy-")
	_, err := Load()
	if err == nil {
		t.Fatal("expected prefix error")
	}
}

func TestLoadRejectsNonLoopbackBind(t *testing.T) {
	t.Setenv("SIDECAR_SUBSCRIPTION_FILE", "x.yaml")
	t.Setenv("SIDECAR_DRY_RUN", "1")
	t.Setenv("SIDECAR_NAME_PREFIX", "sidecar-a-")
	t.Setenv("SIDECAR_BIND_ADDRESS", "0.0.0.0")
	t.Setenv("SIDECAR_ALLOW_NON_LOCAL_BIND", "0")
	_, err := Load()
	if err == nil {
		t.Fatal("expected non-loopback bind error")
	}
}

func TestLoadAcceptsHTTPProtocol(t *testing.T) {
	t.Setenv("SIDECAR_SUBSCRIPTION_FILE", "x.yaml")
	t.Setenv("SIDECAR_DRY_RUN", "1")
	t.Setenv("SIDECAR_PROTOCOL", "http")
	t.Setenv("SIDECAR_NAME_PREFIX", "sidecar-a-")
	t.Setenv("SIDECAR_MAX_PORTS", "5")
	t.Setenv("SIDECAR_BASE_PORT", "22000")
	t.Setenv("SIDECAR_BIND_ADDRESS", "127.0.0.1")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Protocol != "http" || cfg.MaxPorts != 5 || cfg.BasePort != 22000 {
		t.Fatalf("%+v", cfg)
	}
}

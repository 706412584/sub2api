package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultNamePrefix   = "sidecar-a-"
	DefaultBaseURL      = "http://127.0.0.1:18080"
	DefaultProtocol     = "socks5"
	DefaultBasePort     = 21080
	DefaultMaxPorts     = 20
	DefaultSyncInterval = 5 * time.Minute
	DefaultBindAddress  = "127.0.0.1"
)

// Config is loaded only from environment variables. No subscription URL defaults.
type Config struct {
	SubscriptionURL            string
	SubscriptionFile           string
	BaseURL                    string
	AdminAPIKey                string
	NamePrefix                 string
	Protocol                   string
	BindAddress                string
	BasePort                   int
	MaxPorts                   int
	SyncInterval               time.Duration
	MihomoBinary               string
	MihomoConfigPath           string
	MihomoDataDir              string
	DryRun                     bool
	SkipEngine                 bool
	AllowInsecureSubscription  bool
	AllowNonLocalBind          bool
}

func Load() (*Config, error) {
	cfg := &Config{
		SubscriptionURL:           strings.TrimSpace(os.Getenv("SIDECAR_SUBSCRIPTION_URL")),
		SubscriptionFile:          strings.TrimSpace(os.Getenv("SIDECAR_SUBSCRIPTION_FILE")),
		BaseURL:                   strings.TrimSpace(os.Getenv("SIDECAR_SUB2API_BASE_URL")),
		AdminAPIKey:               strings.TrimSpace(os.Getenv("SIDECAR_ADMIN_API_KEY")),
		NamePrefix:                strings.TrimSpace(os.Getenv("SIDECAR_NAME_PREFIX")),
		Protocol:                  strings.TrimSpace(os.Getenv("SIDECAR_PROTOCOL")),
		BindAddress:               strings.TrimSpace(os.Getenv("SIDECAR_BIND_ADDRESS")),
		MihomoBinary:              strings.TrimSpace(os.Getenv("SIDECAR_MIHOMO_BINARY")),
		MihomoConfigPath:          strings.TrimSpace(os.Getenv("SIDECAR_MIHOMO_CONFIG_PATH")),
		MihomoDataDir:             strings.TrimSpace(os.Getenv("SIDECAR_MIHOMO_DATA_DIR")),
		DryRun:                    envBool("SIDECAR_DRY_RUN", false),
		SkipEngine:                envBool("SIDECAR_SKIP_ENGINE", false),
		AllowInsecureSubscription: envBool("SIDECAR_ALLOW_INSECURE_SUBSCRIPTION", false),
		AllowNonLocalBind:         envBool("SIDECAR_ALLOW_NON_LOCAL_BIND", false),
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.NamePrefix == "" {
		cfg.NamePrefix = DefaultNamePrefix
	}
	if cfg.Protocol == "" {
		cfg.Protocol = DefaultProtocol
	}
	if cfg.BindAddress == "" {
		cfg.BindAddress = DefaultBindAddress
	}
	if cfg.MihomoBinary == "" {
		cfg.MihomoBinary = "mihomo"
	}
	if cfg.MihomoConfigPath == "" {
		cfg.MihomoConfigPath = "data/mihomo-config.yaml"
	}
	if cfg.MihomoDataDir == "" {
		cfg.MihomoDataDir = "data/mihomo"
	}

	cfg.BasePort = envInt("SIDECAR_BASE_PORT", DefaultBasePort)
	cfg.MaxPorts = envInt("SIDECAR_MAX_PORTS", DefaultMaxPorts)
	cfg.SyncInterval = envDuration("SIDECAR_SYNC_INTERVAL", DefaultSyncInterval)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.SubscriptionURL == "" && c.SubscriptionFile == "" {
		return fmt.Errorf("set SIDECAR_SUBSCRIPTION_URL or SIDECAR_SUBSCRIPTION_FILE")
	}
	if !c.DryRun && c.AdminAPIKey == "" {
		return fmt.Errorf("SIDECAR_ADMIN_API_KEY is required unless SIDECAR_DRY_RUN=1")
	}
	switch c.Protocol {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("unsupported SIDECAR_PROTOCOL %q", c.Protocol)
	}
	if c.BasePort < 1 || c.BasePort > 65535 {
		return fmt.Errorf("invalid SIDECAR_BASE_PORT")
	}
	if c.MaxPorts < 1 || c.MaxPorts > 500 {
		return fmt.Errorf("SIDECAR_MAX_PORTS must be 1..500")
	}
	if c.BasePort+c.MaxPorts-1 > 65535 {
		return fmt.Errorf("port range exceeds 65535")
	}
	if c.SyncInterval <= 0 {
		return fmt.Errorf("SIDECAR_SYNC_INTERVAL must be greater than 0")
	}
	if !strings.HasPrefix(c.NamePrefix, "sidecar-") {
		return fmt.Errorf("SIDECAR_NAME_PREFIX must start with sidecar-")
	}
	if err := validateBind(c.BindAddress, c.AllowNonLocalBind); err != nil {
		return err
	}
	return nil
}

func validateBind(addr string, allowNonLocal bool) error {
	if allowNonLocal {
		return nil
	}
	if addr == "localhost" || addr == "127.0.0.1" || addr == "::1" {
		return nil
	}
	ip := net.ParseIP(addr)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("SIDECAR_BIND_ADDRESS %q must be loopback unless SIDECAR_ALLOW_NON_LOCAL_BIND=1", addr)
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

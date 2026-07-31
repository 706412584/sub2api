package service

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	ProxySubscriptionSourceURL    = "url"
	ProxySubscriptionSourceInline = "inline"

	ProxySubscriptionStatusIdle    = ""
	ProxySubscriptionStatusOK      = "ok"
	ProxySubscriptionStatusError   = "error"
	ProxySubscriptionStatusRunning = "running"

	// ManagedProxyNamePrefix is required for all embedded-subscription owned proxies.
	ManagedProxyNamePrefix = "sidecar-"
)

var (
	ErrProxySubscriptionNotFound = infraerrors.NotFound("PROXY_SUBSCRIPTION_NOT_FOUND", "proxy subscription not found")
	ErrProxySubscriptionInvalid  = infraerrors.BadRequest("PROXY_SUBSCRIPTION_INVALID", "proxy subscription invalid")
	ErrProxySubscriptionConflict = infraerrors.Conflict("PROXY_SUBSCRIPTION_CONFLICT", "proxy subscription conflict")
)

// ProxySubscription is the service-layer model for an embedded subscription source.
type ProxySubscription struct {
	ID                    int64
	Name                  string
	Enabled               bool
	SourceType            string
	SubscriptionURL       string
	InlineBody            string
	NamePrefix            string
	Protocol              string
	BindAddress           string
	BasePort              int
	MaxPorts              int
	SyncIntervalSec       int
	NodeAllowContains     []string
	NodeIdentityAllowlist []string
	LastSyncAt            *time.Time
	LastSyncStatus        string
	LastSyncError         string
	LastConfigHash        string
	DesiredCount          int
	CreatedBy             int64
	NextDueAt             *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// ProxySubscriptionListParams filters list queries.
type ProxySubscriptionListParams struct {
	Page     int
	PageSize int
	Enabled  *bool
	Search   string
}

// ProxySubscriptionCreateParams creates a source.
type ProxySubscriptionCreateParams struct {
	Name                  string
	Enabled               *bool
	SourceType            string
	SubscriptionURL       string
	InlineBody            string
	NamePrefix            string
	Protocol              string
	BindAddress           string
	BasePort              int
	MaxPorts              int
	SyncIntervalSec       int
	NodeAllowContains     []string
	NodeIdentityAllowlist []string
	CreatedBy             int64
}

// ProxySubscriptionUpdateParams updates a source. Nil pointer means leave unchanged.
type ProxySubscriptionUpdateParams struct {
	Name                  *string
	Enabled               *bool
	SourceType            *string
	SubscriptionURL       *string
	InlineBody            *string
	NamePrefix            *string
	Protocol              *string
	BindAddress           *string
	BasePort              *int
	MaxPorts              *int
	SyncIntervalSec       *int
	NodeAllowContains     *[]string
	NodeIdentityAllowlist *[]string
}

// ProxySubscriptionPreviewNode is a sanitized node entry for admin UI selection.
type ProxySubscriptionPreviewNode struct {
	Identity string `json:"identity"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Server   string `json:"server"`
	Port     string `json:"port"`
}

// ProxySubscriptionPreviewResult is the outcome of preview-nodes.
type ProxySubscriptionPreviewResult struct {
	Nodes              []ProxySubscriptionPreviewNode `json:"nodes"`
	Total              int                            `json:"total"`
	SelectedIdentities []string                       `json:"selected_identities"`
}

// ProxySubscriptionPreviewParams previews nodes from draft source fields (unsaved).
type ProxySubscriptionPreviewParams struct {
	SourceType        string
	SubscriptionURL   string
	InlineBody        string
	NodeAllowContains []string
}

// ProxySubscriptionSyncResult is the outcome of one sync cycle.
type ProxySubscriptionSyncResult struct {
	DesiredCount  int    `json:"desired_count"`
	ConfigHash    string `json:"config_hash"`
	Created       int    `json:"created"`
	Updated       int    `json:"updated"`
	Unchanged     int    `json:"unchanged"`
	Deleted       int    `json:"deleted"`
	Skipped       int    `json:"skipped"`
	EngineRunning bool   `json:"engine_running"`
	EngineSkipped bool   `json:"engine_skipped"`
	Message       string `json:"message,omitempty"`
}

// ProxySubscriptionEngineStatus reports in-process mihomo state.
type ProxySubscriptionEngineStatus struct {
	BinaryPath   string                                `json:"binary_path"`
	BinaryFound  bool                                  `json:"binary_found"`
	DataDir      string                                `json:"data_dir"`
	RunningCount int                                   `json:"running_count"`
	Sources      []ProxySubscriptionEngineSourceStatus `json:"sources"`
}

// ProxySubscriptionEngineSourceStatus is per-source engine status.
type ProxySubscriptionEngineSourceStatus struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	NamePrefix string `json:"name_prefix"`
	Running    bool   `json:"running"`
	ConfigHash string `json:"config_hash"`
	BasePort   int    `json:"base_port"`
	MaxPorts   int    `json:"max_ports"`
	LastError  string `json:"last_error,omitempty"`
}

// ProxySubscriptionRepository persists subscription sources.
type ProxySubscriptionRepository interface {
	Create(ctx context.Context, m *ProxySubscription) error
	GetByID(ctx context.Context, id int64) (*ProxySubscription, error)
	Update(ctx context.Context, m *ProxySubscription) error
	Delete(ctx context.Context, id int64) error
	List(ctx context.Context, params ProxySubscriptionListParams) ([]*ProxySubscription, int64, error)
	ListEnabled(ctx context.Context) ([]*ProxySubscription, error)
	ListDue(ctx context.Context, now time.Time, limit int) ([]*ProxySubscription, error)
	UpdateSyncState(ctx context.Context, id int64, status, errMsg, configHash string, desiredCount int, lastSyncAt, nextDueAt *time.Time) error
	ExistsNamePrefix(ctx context.Context, prefix string, excludeID int64) (bool, error)
	ListNamePrefixes(ctx context.Context) ([]string, error)
}

package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/proxysubscription"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type proxySubscriptionRepository struct {
	client *dbent.Client
}

// NewProxySubscriptionRepository creates the repository.
func NewProxySubscriptionRepository(client *dbent.Client) service.ProxySubscriptionRepository {
	return &proxySubscriptionRepository{client: client}
}

func (r *proxySubscriptionRepository) Create(ctx context.Context, m *service.ProxySubscription) error {
	client := clientFromContext(ctx, r.client)
	builder := client.ProxySubscription.Create().
		SetName(m.Name).
		SetEnabled(m.Enabled).
		SetSourceType(m.SourceType).
		SetSubscriptionURL(m.SubscriptionURL).
		SetInlineBody(m.InlineBody).
		SetNamePrefix(m.NamePrefix).
		SetProtocol(m.Protocol).
		SetBindAddress(m.BindAddress).
		SetBasePort(m.BasePort).
		SetMaxPorts(m.MaxPorts).
		SetSyncIntervalSec(m.SyncIntervalSec).
		SetNodeAllowContains(emptyStringSlice(m.NodeAllowContains)).
		SetLastSyncStatus(m.LastSyncStatus).
		SetLastSyncError(m.LastSyncError).
		SetLastConfigHash(m.LastConfigHash).
		SetDesiredCount(m.DesiredCount).
		SetCreatedBy(m.CreatedBy)
	if m.LastSyncAt != nil {
		builder = builder.SetLastSyncAt(*m.LastSyncAt)
	}
	if m.NextDueAt != nil {
		builder = builder.SetNextDueAt(*m.NextDueAt)
	}
	created, err := builder.Save(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrProxySubscriptionNotFound, service.ErrProxySubscriptionConflict)
	}
	*m = *entToProxySubscription(created)
	return nil
}

func (r *proxySubscriptionRepository) GetByID(ctx context.Context, id int64) (*service.ProxySubscription, error) {
	client := clientFromContext(ctx, r.client)
	row, err := client.ProxySubscription.Get(ctx, id)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrProxySubscriptionNotFound, nil)
	}
	return entToProxySubscription(row), nil
}

func (r *proxySubscriptionRepository) Update(ctx context.Context, m *service.ProxySubscription) error {
	client := clientFromContext(ctx, r.client)
	updater := client.ProxySubscription.UpdateOneID(m.ID).
		SetName(m.Name).
		SetEnabled(m.Enabled).
		SetSourceType(m.SourceType).
		SetSubscriptionURL(m.SubscriptionURL).
		SetInlineBody(m.InlineBody).
		SetNamePrefix(m.NamePrefix).
		SetProtocol(m.Protocol).
		SetBindAddress(m.BindAddress).
		SetBasePort(m.BasePort).
		SetMaxPorts(m.MaxPorts).
		SetSyncIntervalSec(m.SyncIntervalSec).
		SetNodeAllowContains(emptyStringSlice(m.NodeAllowContains)).
		SetLastSyncStatus(m.LastSyncStatus).
		SetLastSyncError(m.LastSyncError).
		SetLastConfigHash(m.LastConfigHash).
		SetDesiredCount(m.DesiredCount)
	if m.LastSyncAt != nil {
		updater = updater.SetLastSyncAt(*m.LastSyncAt)
	} else {
		updater = updater.ClearLastSyncAt()
	}
	if m.NextDueAt != nil {
		updater = updater.SetNextDueAt(*m.NextDueAt)
	} else {
		updater = updater.ClearNextDueAt()
	}
	updated, err := updater.Save(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrProxySubscriptionNotFound, service.ErrProxySubscriptionConflict)
	}
	m.UpdatedAt = updated.UpdatedAt
	return nil
}

func (r *proxySubscriptionRepository) Delete(ctx context.Context, id int64) error {
	client := clientFromContext(ctx, r.client)
	if err := client.ProxySubscription.DeleteOneID(id).Exec(ctx); err != nil {
		return translatePersistenceError(err, service.ErrProxySubscriptionNotFound, nil)
	}
	return nil
}

func (r *proxySubscriptionRepository) List(ctx context.Context, params service.ProxySubscriptionListParams) ([]*service.ProxySubscription, int64, error) {
	client := clientFromContext(ctx, r.client)
	q := client.ProxySubscription.Query()
	if params.Enabled != nil {
		q = q.Where(proxysubscription.EnabledEQ(*params.Enabled))
	}
	if s := strings.TrimSpace(params.Search); s != "" {
		q = q.Where(proxysubscription.Or(
			proxysubscription.NameContainsFold(s),
			proxysubscription.NamePrefixContainsFold(s),
		))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count proxy subscriptions: %w", err)
	}
	pageSize := params.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	page := params.Page
	if page <= 0 {
		page = 1
	}
	rows, err := q.
		Order(dbent.Desc(proxysubscription.FieldID)).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("list proxy subscriptions: %w", err)
	}
	out := make([]*service.ProxySubscription, 0, len(rows))
	for _, row := range rows {
		out = append(out, entToProxySubscription(row))
	}
	return out, int64(total), nil
}

func (r *proxySubscriptionRepository) ListEnabled(ctx context.Context) ([]*service.ProxySubscription, error) {
	client := clientFromContext(ctx, r.client)
	rows, err := client.ProxySubscription.Query().
		Where(proxysubscription.EnabledEQ(true)).
		Order(dbent.Asc(proxysubscription.FieldID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list enabled proxy subscriptions: %w", err)
	}
	out := make([]*service.ProxySubscription, 0, len(rows))
	for _, row := range rows {
		out = append(out, entToProxySubscription(row))
	}
	return out, nil
}

func (r *proxySubscriptionRepository) ListDue(ctx context.Context, now time.Time, limit int) ([]*service.ProxySubscription, error) {
	if limit <= 0 {
		limit = 20
	}
	client := clientFromContext(ctx, r.client)
	rows, err := client.ProxySubscription.Query().
		Where(
			proxysubscription.EnabledEQ(true),
			proxysubscription.Or(
				proxysubscription.NextDueAtIsNil(),
				proxysubscription.NextDueAtLTE(now),
			),
		).
		Order(dbent.Asc(proxysubscription.FieldNextDueAt), dbent.Asc(proxysubscription.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list due proxy subscriptions: %w", err)
	}
	out := make([]*service.ProxySubscription, 0, len(rows))
	for _, row := range rows {
		out = append(out, entToProxySubscription(row))
	}
	return out, nil
}

func (r *proxySubscriptionRepository) UpdateSyncState(
	ctx context.Context,
	id int64,
	status, errMsg, configHash string,
	desiredCount int,
	lastSyncAt, nextDueAt *time.Time,
) error {
	client := clientFromContext(ctx, r.client)
	updater := client.ProxySubscription.UpdateOneID(id).
		SetLastSyncStatus(status).
		SetLastSyncError(errMsg).
		SetLastConfigHash(configHash).
		SetDesiredCount(desiredCount)
	if lastSyncAt != nil {
		updater = updater.SetLastSyncAt(*lastSyncAt)
	}
	if nextDueAt != nil {
		updater = updater.SetNextDueAt(*nextDueAt)
	} else {
		updater = updater.ClearNextDueAt()
	}
	if err := updater.Exec(ctx); err != nil {
		return translatePersistenceError(err, service.ErrProxySubscriptionNotFound, nil)
	}
	return nil
}

func (r *proxySubscriptionRepository) ExistsNamePrefix(ctx context.Context, prefix string, excludeID int64) (bool, error) {
	client := clientFromContext(ctx, r.client)
	q := client.ProxySubscription.Query().Where(proxysubscription.NamePrefixEQ(prefix))
	if excludeID > 0 {
		q = q.Where(proxysubscription.IDNEQ(excludeID))
	}
	n, err := q.Count(ctx)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (r *proxySubscriptionRepository) ListNamePrefixes(ctx context.Context) ([]string, error) {
	client := clientFromContext(ctx, r.client)
	rows, err := client.ProxySubscription.Query().
		Select(proxysubscription.FieldNamePrefix).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.NamePrefix)
	}
	return out, nil
}

func entToProxySubscription(row *dbent.ProxySubscription) *service.ProxySubscription {
	if row == nil {
		return nil
	}
	return &service.ProxySubscription{
		ID:                row.ID,
		Name:              row.Name,
		Enabled:           row.Enabled,
		SourceType:        row.SourceType,
		SubscriptionURL:   row.SubscriptionURL,
		InlineBody:        row.InlineBody,
		NamePrefix:        row.NamePrefix,
		Protocol:          row.Protocol,
		BindAddress:       row.BindAddress,
		BasePort:          row.BasePort,
		MaxPorts:          row.MaxPorts,
		SyncIntervalSec:   row.SyncIntervalSec,
		NodeAllowContains: append([]string(nil), row.NodeAllowContains...),
		LastSyncAt:        row.LastSyncAt,
		LastSyncStatus:    row.LastSyncStatus,
		LastSyncError:     row.LastSyncError,
		LastConfigHash:    row.LastConfigHash,
		DesiredCount:      row.DesiredCount,
		CreatedBy:         row.CreatedBy,
		NextDueAt:         row.NextDueAt,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}

func emptyStringSlice(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

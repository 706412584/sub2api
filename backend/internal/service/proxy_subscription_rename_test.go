//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/clashsub"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

// applyProxiesRepoStub 只实现 applyProxies 用到的三个方法，其余 panic 暴露误用。
type applyProxiesRepoStub struct {
	owned   []ProxyWithAccountCount
	updated []Proxy
	created []Proxy
	deleted []int64
	nextID  int64
}

func (r *applyProxiesRepoStub) ListOwnedByPrefix(context.Context, string) ([]ProxyWithAccountCount, error) {
	return r.owned, nil
}

func (r *applyProxiesRepoStub) Update(_ context.Context, p *Proxy) error {
	r.updated = append(r.updated, *p)
	return nil
}

func (r *applyProxiesRepoStub) Create(_ context.Context, p *Proxy) error {
	r.nextID++
	p.ID = r.nextID
	r.created = append(r.created, *p)
	return nil
}

func (r *applyProxiesRepoStub) Delete(_ context.Context, id int64) error {
	r.deleted = append(r.deleted, id)
	return nil
}

func (r *applyProxiesRepoStub) GetByID(context.Context, int64) (*Proxy, error) {
	panic("unexpected GetByID")
}
func (r *applyProxiesRepoStub) ListByIDs(context.Context, []int64) ([]Proxy, error) {
	panic("unexpected ListByIDs")
}
func (r *applyProxiesRepoStub) List(context.Context, pagination.PaginationParams) ([]Proxy, *pagination.PaginationResult, error) {
	panic("unexpected List")
}
func (r *applyProxiesRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string) ([]Proxy, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters")
}
func (r *applyProxiesRepoStub) ListWithFiltersAndAccountCount(context.Context, pagination.PaginationParams, string, string, string) ([]ProxyWithAccountCount, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFiltersAndAccountCount")
}
func (r *applyProxiesRepoStub) ListActive(context.Context) ([]Proxy, error) {
	panic("unexpected ListActive")
}
func (r *applyProxiesRepoStub) ListActiveWithAccountCount(context.Context) ([]ProxyWithAccountCount, error) {
	panic("unexpected ListActiveWithAccountCount")
}
func (r *applyProxiesRepoStub) ExistsByHostPortAuth(context.Context, string, int, string, string) (bool, error) {
	panic("unexpected ExistsByHostPortAuth")
}
func (r *applyProxiesRepoStub) CountAccountsByProxyID(context.Context, int64) (int64, error) {
	panic("unexpected CountAccountsByProxyID")
}
func (r *applyProxiesRepoStub) ListAccountSummariesByProxyID(context.Context, int64) ([]ProxyAccountSummary, error) {
	panic("unexpected ListAccountSummariesByProxyID")
}
func (r *applyProxiesRepoStub) SweepExpiredProxies(context.Context, time.Time) (int64, error) {
	panic("unexpected SweepExpiredProxies")
}
func (r *applyProxiesRepoStub) ListAllForFallback(context.Context) ([]Proxy, error) {
	panic("unexpected ListAllForFallback")
}
func (r *applyProxiesRepoStub) CountExpired(context.Context) (int64, error) {
	panic("unexpected CountExpired")
}
func (r *applyProxiesRepoStub) CountExpiringSoon(context.Context, time.Time) (int64, error) {
	panic("unexpected CountExpiringSoon")
}

// 可读片段规则变化（节点名开始保留中文）后，同一节点的 hash8 不变：
// 必须原地重命名而不是删旧建新，否则已绑定账号的代理会被重建（或因
// account_count>0 被跳过，留下重名的僵尸行）。
func TestApplyProxies_RenamesOnFragmentRuleChange(t *testing.T) {
	const prefix = "sidecar-a-"
	identity := "vless|aws-link30.lxyun.xyz|23568|🇸🇬新加坡高速04|bgp|ctcucm"
	oldName := prefix + clashsubHash(t, prefix, identity) + "-04BGPCTCUCM"
	newName := clashsub.StableProxyName(prefix, identity, "🇸🇬新加坡高速04|BGP|CTCUCM")
	require.NotEqual(t, oldName, newName, "前提：改名后名字必须不同")

	repo := &applyProxiesRepoStub{
		owned: []ProxyWithAccountCount{{
			Proxy:        Proxy{ID: 23037, Name: oldName, Protocol: "http", Host: "127.0.0.1", Port: 27890, Status: StatusActive},
			AccountCount: 1,
		}},
		nextID: 90000,
	}
	svc := &ProxySubscriptionService{proxyRepo: repo}
	res := &ProxySubscriptionSyncResult{}

	err := svc.applyProxies(context.Background(), prefix, []clashsub.LocalEndpoint{{
		Name: newName, Port: 27890, Protocol: "http", Host: "127.0.0.1",
	}}, res)
	require.NoError(t, err)

	require.Empty(t, repo.created, "不得新建代理行")
	require.Empty(t, repo.deleted, "不得删除已绑定账号的代理行")
	require.Len(t, repo.updated, 1)
	require.Equal(t, int64(23037), repo.updated[0].ID, "必须复用原有代理 ID，账号绑定才不会断")
	require.Equal(t, newName, repo.updated[0].Name)
	require.Equal(t, 1, res.Updated)
	require.Zero(t, res.Created)
	require.Zero(t, res.Deleted)
	require.Zero(t, res.Skipped)
}

// 节点真的消失（hash8 不在期望集合里）时仍应删除，不能被身份复用逻辑挡住。
func TestApplyProxies_StillPrunesVanishedNodes(t *testing.T) {
	const prefix = "sidecar-a-"
	keptIdentity := "vless|a|1|N1"
	goneIdentity := "vless|b|2|N2"
	keptName := clashsub.StableProxyName(prefix, keptIdentity, "新加坡01")
	goneName := clashsub.StableProxyName(prefix, goneIdentity, "日本02")

	repo := &applyProxiesRepoStub{
		owned: []ProxyWithAccountCount{
			{Proxy: Proxy{ID: 1, Name: keptName, Protocol: "http", Host: "127.0.0.1", Port: 27890, Status: StatusActive}},
			{Proxy: Proxy{ID: 2, Name: goneName, Protocol: "http", Host: "127.0.0.1", Port: 27891, Status: StatusActive}},
		},
		nextID: 100,
	}
	svc := &ProxySubscriptionService{proxyRepo: repo}
	res := &ProxySubscriptionSyncResult{}

	err := svc.applyProxies(context.Background(), prefix, []clashsub.LocalEndpoint{{
		Name: keptName, Port: 27890, Protocol: "http", Host: "127.0.0.1",
	}}, res)
	require.NoError(t, err)

	require.Equal(t, []int64{2}, repo.deleted)
	require.Equal(t, 1, res.Unchanged)
	require.Equal(t, 1, res.Deleted)
	require.Empty(t, repo.created)
}

// clashsubHash 取 StableProxyName 生成的 hash8，用于拼历史命名格式。
func clashsubHash(t *testing.T, prefix, identity string) string {
	t.Helper()
	name := clashsub.StableProxyName(prefix, identity, "x")
	return name[len(prefix) : len(prefix)+8]
}

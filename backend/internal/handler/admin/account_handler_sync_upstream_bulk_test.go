package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupSyncUpstreamBulkRouter(adminSvc *stubAdminService) *gin.Engine {
	return setupSyncUpstreamBulkRouterWithTestService(adminSvc, nil)
}

// 传入非 nil 的 accountTestService 可越过依赖检查，进入目标解析分支。
func setupSyncUpstreamBulkRouterWithTestService(adminSvc *stubAdminService, testSvc *service.AccountTestService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, testSvc, nil, nil, nil, nil, nil)
	router.POST("/api/v1/admin/accounts/models/sync-upstream-bulk", handler.SyncUpstreamModelsBulk)
	return router
}

// 缺少 account_ids 与 filters 时必须拒绝，避免误把「全库」当目标。
func TestSyncUpstreamModelsBulkRequiresTargets(t *testing.T) {
	adminSvc := newStubAdminService()
	router := setupSyncUpstreamBulkRouter(adminSvc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/models/sync-upstream-bulk",
		bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// accountTestService 未注入时不能静默成功。
func TestSyncUpstreamModelsBulkRequiresTestService(t *testing.T) {
	adminSvc := newStubAdminService()
	router := setupSyncUpstreamBulkRouter(adminSvc)

	body, _ := json.Marshal(map[string]any{"account_ids": []int64{1, 2}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/models/sync-upstream-bulk",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

// filters 模式：目标由服务端解析，不要求前端逐个传 ID。
func TestSyncUpstreamModelsBulkAcceptsFilters(t *testing.T) {
	adminSvc := newStubAdminService()
	adminSvc.resolveBulkUpdateTargetIDsResult = []int64{7, 8}
	router := setupSyncUpstreamBulkRouterWithTestService(adminSvc, &service.AccountTestService{})

	body, _ := json.Marshal(map[string]any{
		"filters": map[string]any{"platform": "grok"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/models/sync-upstream-bulk",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	// 该测试关注 filters 被解析（stub 已记录），而非下游同步成功。
	require.NotNil(t, adminSvc.lastBulkUpdateFilters)
	require.Equal(t, "grok", adminSvc.lastBulkUpdateFilters.Platform)
}

// 只增不删：上游只声明 1 个模型时，账号已有的完整映射必须保留。
// 这是「同步后测试下拉只剩 grok-4.7」的回归护栏。
func TestMergeUpstreamModelsIntoMappingKeepsExisting(t *testing.T) {
	existing := map[string]string{
		"grok-4.3": "grok-4.3",
		"grok-4.5": "grok-4.5",
		"grok-4.6": "grok-4.6",
	}
	merged, added := mergeUpstreamModelsIntoMapping(existing, []string{"grok-4.7"})

	require.Equal(t, 1, added)
	require.Len(t, merged, 4, "已有 3 个模型 + 新增 1 个")
	for _, key := range []string{"grok-4.3", "grok-4.5", "grok-4.6", "grok-4.7"} {
		require.Contains(t, merged, key)
	}
}

// 上游模型已全部存在时，added 为 0（调用方据此报「无需更新」且不写库）。
func TestMergeUpstreamModelsIntoMappingNoChanges(t *testing.T) {
	existing := map[string]string{"grok-4.7": "grok-4.7"}
	merged, added := mergeUpstreamModelsIntoMapping(existing, []string{"grok-4.7"})

	require.Equal(t, 0, added)
	require.Len(t, merged, 1)
}

// 空白与重复输入不得产生多余条目。
func TestMergeUpstreamModelsIntoMappingSkipsBlanksAndDuplicates(t *testing.T) {
	merged, added := mergeUpstreamModelsIntoMapping(nil, []string{"grok-4.7", " grok-4.7 ", "", "  ", "grok-4.6"})

	require.Equal(t, 2, added)
	require.Len(t, merged, 2)
	require.Contains(t, merged, "grok-4.7")
	require.Contains(t, merged, "grok-4.6")
}

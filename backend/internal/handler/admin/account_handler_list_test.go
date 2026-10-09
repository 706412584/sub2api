package admin

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountHandlerListLiteUsesCompactDTOAndETag(t *testing.T) {
	router, adminSvc := setupAccountListRouter()
	now := time.Now().UTC()
	groupID := int64(77)
	adminSvc.accounts = []service.Account{{
		ID: 501, Name: "compact-account", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"email": "compact@example.com", "access_token": strings.Repeat("x", 4096)},
		Extra:       map[string]any{"privacy_mode": "training_off"}, Status: service.StatusActive,
		Schedulable: true, Concurrency: 4, GroupIDs: []int64{groupID},
		Groups:        []*service.Group{{ID: groupID, Name: "codex", Platform: service.PlatformOpenAI}},
		AccountGroups: []service.AccountGroup{{AccountID: 501, GroupID: groupID, Priority: 2, Group: &service.Group{ID: groupID, Name: "codex", Platform: service.PlatformOpenAI}}},
		CreatedAt:     now, UpdatedAt: now,
	}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20&lite=1", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotEmpty(t, rec.Header().Get("ETag"))

	var litePayload struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &litePayload))
	require.Len(t, litePayload.Data.Items, 1)
	liteItem := litePayload.Data.Items[0]
	require.Equal(t, float64(501), liteItem["id"])
	require.Equal(t, []any{float64(groupID)}, liteItem["group_ids"])
	require.Equal(t, true, liteItem["schedulable"])
	require.NotContains(t, liteItem, "groups")
	require.NotContains(t, liteItem, "account_groups")
	credentials, ok := liteItem["credentials"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "compact@example.com", credentials["email"])
	require.NotContains(t, credentials, "access_token")
	credentialsStatus, ok := liteItem["credentials_status"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, credentialsStatus["has_access_token"])

	// The ETag must represent the same compact body and return 304 on refresh.
	rec304 := httptest.NewRecorder()
	req304 := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20&lite=1", nil)
	req304.Header.Set("If-None-Match", rec.Header().Get("ETag"))
	router.ServeHTTP(rec304, req304)
	require.Equal(t, http.StatusNotModified, rec304.Code)

	// Omitting lite preserves the legacy full response shape.
	recFull := httptest.NewRecorder()
	reqFull := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20", nil)
	router.ServeHTTP(recFull, reqFull)
	require.Equal(t, http.StatusOK, recFull.Code)
	var fullPayload struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recFull.Body.Bytes(), &fullPayload))
	require.Contains(t, fullPayload.Data.Items[0], "groups")
	require.Contains(t, fullPayload.Data.Items[0], "account_groups")
}

func TestAccountHandlerListLiteStaysBelowResponseBudget(t *testing.T) {
	router, adminSvc := setupAccountListRouter()
	now := time.Now().UTC()
	accounts := make([]service.Account, 20)
	for i := range accounts {
		id := int64(600 + i)
		groupID := int64(800 + i)
		accounts[i] = service.Account{
			ID: id, Name: "account-" + strconv.Itoa(i), Platform: service.PlatformOpenAI,
			Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true,
			Concurrency: 4, GroupIDs: []int64{groupID},
			Groups:        []*service.Group{{ID: groupID, Name: "group-" + strconv.Itoa(i), Description: strings.Repeat("description ", 20)}},
			AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID, Group: &service.Group{ID: groupID, Name: "group-" + strconv.Itoa(i), Description: strings.Repeat("description ", 20)}}},
			CreatedAt:     now, UpdatedAt: now,
		}
	}
	adminSvc.accounts = accounts

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20&lite=1", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Less(t, rec.Body.Len(), 80*1024)

	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, err := zw.Write(rec.Body.Bytes())
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.Less(t, compressed.Len(), 15*1024)
}

func setupAccountListRouter() (*gin.Engine, *stubAdminService) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	adminSvc := newStubAdminService()
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.GET("/api/v1/admin/accounts", handler.List)
	return router, adminSvc
}

func TestNormalizeBatchTestAccountIDs(t *testing.T) {
	t.Run("rejects empty input", func(t *testing.T) {
		_, err := normalizeBatchTestAccountIDs(nil)
		require.EqualError(t, err, "account_ids is required")
	})

	t.Run("deduplicates while preserving order", func(t *testing.T) {
		ids, err := normalizeBatchTestAccountIDs([]int64{3, 1, 3, 2, 1})
		require.NoError(t, err)
		require.Equal(t, []int64{3, 1, 2}, ids)
	})

	t.Run("rejects invalid IDs", func(t *testing.T) {
		_, err := normalizeBatchTestAccountIDs([]int64{1, 0})
		require.EqualError(t, err, "account_ids must contain positive IDs")
	})

	t.Run("accepts exactly the limit", func(t *testing.T) {
		requested := make([]int64, batchTestAccountsMax)
		for i := range requested {
			requested[i] = int64(i + 1)
		}
		ids, err := normalizeBatchTestAccountIDs(requested)
		require.NoError(t, err)
		require.Len(t, ids, batchTestAccountsMax)
	})

	t.Run("rejects too many unique IDs", func(t *testing.T) {
		requested := make([]int64, batchTestAccountsMax+1)
		for i := range requested {
			requested[i] = int64(i + 1)
		}
		_, err := normalizeBatchTestAccountIDs(requested)
		require.EqualError(t, err, "account_ids cannot exceed 100 unique IDs")
	})
}

func TestBatchTestProxyOverrideAndSchedule(t *testing.T) {
	proxyID := int64(31)
	otherProxyID := int64(37)
	openaiAccount := &service.Account{ID: 1, Platform: service.PlatformOpenAI, ProxyID: &proxyID}
	grokAccount := &service.Account{ID: 2, Platform: service.PlatformGrok, ProxyID: &otherProxyID}
	directAccount := &service.Account{ID: 3, Platform: service.PlatformOpenAI, ProxyID: nil}

	t.Run("nil override keeps bound proxy", func(t *testing.T) {
		override, err := resolveBatchTestProxyOverride(context.Background(), nil, nil)
		require.NoError(t, err)
		require.Nil(t, override)
	})

	t.Run("zero override forces direct", func(t *testing.T) {
		zero := int64(0)
		override, err := resolveBatchTestProxyOverride(context.Background(), nil, &zero)
		require.NoError(t, err)
		require.NotNil(t, override)
		require.True(t, override.ForceDirect)
		require.Nil(t, override.Proxy)
	})

	t.Run("compact only when all accounts are openai", func(t *testing.T) {
		mode, err := resolveBatchTestMode(service.AccountTestModeCompact, []*service.Account{openaiAccount, directAccount})
		require.NoError(t, err)
		require.Equal(t, service.AccountTestModeCompact, mode)

		_, err = resolveBatchTestMode(service.AccountTestModeCompact, []*service.Account{openaiAccount, grokAccount})
		require.EqualError(t, err, "compact mode is only supported when all selected accounts are OpenAI")
	})

	t.Run("grok defaults to serial interval", func(t *testing.T) {
		concurrency, intervalMs, err := resolveBatchTestSchedule([]*service.Account{openaiAccount, grokAccount}, nil, nil)
		require.NoError(t, err)
		require.Equal(t, batchTestGrokDefaultConcurrency, concurrency)
		require.Equal(t, batchTestGrokDefaultIntervalMs, intervalMs)

		concurrency, intervalMs, err = resolveBatchTestSchedule([]*service.Account{openaiAccount}, nil, nil)
		require.NoError(t, err)
		require.Equal(t, batchTestDefaultConcurrency, concurrency)
		require.Equal(t, batchTestDefaultIntervalMs, intervalMs)

		overrideConcurrency := 3
		overrideInterval := 1500
		concurrency, intervalMs, err = resolveBatchTestSchedule([]*service.Account{grokAccount}, &overrideConcurrency, &overrideInterval)
		require.NoError(t, err)
		require.Equal(t, 3, concurrency)
		require.Equal(t, 1500, intervalMs)
	})
}

func TestDefaultAccountPoolProbeSettingsDisabled(t *testing.T) {
	settings := service.DefaultAccountPoolProbeSettings()
	require.NotNil(t, settings)
	require.False(t, settings.Enabled)
}

func TestAccountHandlerListIncludesCreatedAt(t *testing.T) {
	router, adminSvc := setupAccountListRouter()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20&sort_by=created_at&sort_order=desc", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "created_at", adminSvc.lastListAccounts.sortBy)

	var payload struct {
		Data struct {
			Items []struct {
				ID        int64  `json:"id"`
				CreatedAt string `json:"created_at"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 1)

	createdAt := payload.Data.Items[0].CreatedAt
	require.NotEmpty(t, createdAt)
	require.True(t, strings.HasSuffix(createdAt, "Z"), "created_at should be serialized as UTC")
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	require.NoError(t, err)
	_, offset := parsed.Zone()
	require.Equal(t, 0, offset)
}

func TestAccountHandlerListReturnsSchedulerScoresPerGroup(t *testing.T) {
	router, adminSvc := setupAccountListRouter()
	now := time.Now().UTC()
	groupID := int64(41)
	adminSvc.accounts = []service.Account{
		{
			ID:          101,
			Name:        "account-high-priority",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 10,
			Priority:    1,
			AccountGroups: []service.AccountGroup{
				{AccountID: 101, GroupID: groupID, Priority: 100, Group: &service.Group{ID: groupID, Name: "openai"}},
			},
			GroupIDs:  []int64{groupID},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:          102,
			Name:        "account-low-priority",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 10,
			Priority:    100000,
			AccountGroups: []service.AccountGroup{
				{AccountID: 102, GroupID: groupID, Priority: 1, Group: &service.Group{ID: groupID, Name: "openai"}},
			},
			GroupIDs:  []int64{groupID},
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20&platform=openai&include_scheduler_score=1", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload struct {
		Data struct {
			Items []struct {
				ID             int64 `json:"id"`
				SchedulerScore struct {
					BaseScore float64 `json:"base_score"`
				} `json:"scheduler_score"`
				SchedulerScores []struct {
					GroupID       *int64  `json:"group_id"`
					GroupName     string  `json:"group_name"`
					GroupPriority *int    `json:"group_priority"`
					BaseScore     float64 `json:"base_score"`
				} `json:"scheduler_scores"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 2)

	var high, low *struct {
		ID             int64 `json:"id"`
		SchedulerScore struct {
			BaseScore float64 `json:"base_score"`
		} `json:"scheduler_score"`
		SchedulerScores []struct {
			GroupID       *int64  `json:"group_id"`
			GroupName     string  `json:"group_name"`
			GroupPriority *int    `json:"group_priority"`
			BaseScore     float64 `json:"base_score"`
		} `json:"scheduler_scores"`
	}
	for i := range payload.Data.Items {
		item := &payload.Data.Items[i]
		switch item.ID {
		case 101:
			high = item
		case 102:
			low = item
		}
	}
	require.NotNil(t, high)
	require.NotNil(t, low)
	require.Len(t, high.SchedulerScores, 1)
	require.Len(t, low.SchedulerScores, 1)
	require.Equal(t, groupID, *high.SchedulerScores[0].GroupID)
	require.Equal(t, "openai", high.SchedulerScores[0].GroupName)
	require.Equal(t, 100, *high.SchedulerScores[0].GroupPriority)
	require.Equal(t, 1, *low.SchedulerScores[0].GroupPriority)
	require.Greater(t, high.SchedulerScores[0].BaseScore, low.SchedulerScores[0].BaseScore)
}

func TestAccountHandlerListSkipsSchedulerScoresByDefault(t *testing.T) {
	router, adminSvc := setupAccountListRouter()
	now := time.Now().UTC()
	adminSvc.accounts = []service.Account{
		{
			ID:          110,
			Name:        "openai-account",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 10,
			Priority:    1,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20&platform=openai", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Zero(t, adminSvc.schedulerScoreFilterCalls)
	require.Zero(t, adminSvc.openAISchedulerScorePoolCalls)

	var payload struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 1)
	require.NotContains(t, payload.Data.Items[0], "scheduler_score")
	require.NotContains(t, payload.Data.Items[0], "scheduler_scores")
}

func TestAccountHandlerListKeepsSchedulerScoreScopedToFilter(t *testing.T) {
	router, adminSvc := setupAccountListRouter()
	now := time.Now().UTC()
	groupID := int64(42)
	visibleAccount := service.Account{
		ID:          201,
		Name:        "visible-low-priority",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 10,
		Priority:    100000,
		AccountGroups: []service.AccountGroup{
			{AccountID: 201, GroupID: groupID, Priority: 1, Group: &service.Group{ID: groupID, Name: "openai"}},
		},
		GroupIDs:  []int64{groupID},
		CreatedAt: now,
		UpdatedAt: now,
	}
	hiddenGroupPeer := service.Account{
		ID:          202,
		Name:        "hidden-high-priority",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 10,
		Priority:    1,
		AccountGroups: []service.AccountGroup{
			{AccountID: 202, GroupID: groupID, Priority: 2, Group: &service.Group{ID: groupID, Name: "openai"}},
		},
		GroupIDs:  []int64{groupID},
		CreatedAt: now,
		UpdatedAt: now,
	}
	adminSvc.accounts = []service.Account{visibleAccount}
	adminSvc.accountSchedulerScoreFilterAccounts = []service.Account{visibleAccount, hiddenGroupPeer}
	adminSvc.openAISchedulerScorePoolAccounts = []service.Account{visibleAccount, hiddenGroupPeer}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=1&platform=openai&include_scheduler_score=1", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload struct {
		Data struct {
			Items []struct {
				ID             int64 `json:"id"`
				SchedulerScore struct {
					BaseScore float64 `json:"base_score"`
				} `json:"scheduler_score"`
				SchedulerScores []struct {
					GroupID   *int64  `json:"group_id"`
					BaseScore float64 `json:"base_score"`
				} `json:"scheduler_scores"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 1)
	item := payload.Data.Items[0]
	require.Equal(t, int64(201), item.ID)
	require.Len(t, item.SchedulerScores, 1)
	require.Equal(t, groupID, *item.SchedulerScores[0].GroupID)
	require.Equal(t, item.SchedulerScores[0].BaseScore, item.SchedulerScore.BaseScore)
}

func TestAccountHandlerListSchedulerScoreIgnoresPagination(t *testing.T) {
	router, adminSvc := setupAccountListRouter()
	now := time.Now().UTC()
	visibleAccount := service.Account{
		ID:          301,
		Name:        "visible-low-priority",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 10,
		Priority:    100000,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	hiddenFilterPeer := service.Account{
		ID:          302,
		Name:        "hidden-high-priority",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 10,
		Priority:    1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	adminSvc.accounts = []service.Account{visibleAccount}
	adminSvc.accountSchedulerScoreFilterAccounts = []service.Account{visibleAccount, hiddenFilterPeer}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=1&platform=openai&include_scheduler_score=1", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload struct {
		Data struct {
			Items []struct {
				ID             int64 `json:"id"`
				SchedulerScore struct {
					BaseScore float64 `json:"base_score"`
				} `json:"scheduler_score"`
				SchedulerScores []struct {
					GroupID   *int64  `json:"group_id"`
					BaseScore float64 `json:"base_score"`
				} `json:"scheduler_scores"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 1)
	require.Equal(t, int64(301), payload.Data.Items[0].ID)
	require.Less(t, payload.Data.Items[0].SchedulerScore.BaseScore, 3.75)
	require.Empty(t, payload.Data.Items[0].SchedulerScores)
}

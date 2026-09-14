//go:build unit

package admin

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TestParseCodebuddyImportData 覆盖导入解析的四种输入形态。
func TestParseCodebuddyImportData(t *testing.T) {
	nested := map[string]any{
		"auth": map[string]any{
			"accessToken":  "at-1",
			"refreshToken": "rt-1",
			"expiresAt":    float64(1770000000),
			"domain":       "www.codebuddy.cn",
		},
		"account": map[string]any{
			"uid":          "u-1",
			"enterpriseId": "e-1",
			"nickname":     "nick-1",
		},
	}

	t.Run("single nested object", func(t *testing.T) {
		accounts, err := parseCodebuddyImportData(nested)
		require.NoError(t, err)
		require.Len(t, accounts, 1)
		require.Equal(t, "at-1", accounts[0].AccessToken)
		require.Equal(t, "u-1", accounts[0].UID)
		require.Equal(t, int64(1770000000), accounts[0].ExpiresAt)
	})

	t.Run("array of flat objects", func(t *testing.T) {
		accounts, err := parseCodebuddyImportData([]any{
			map[string]any{"accessToken": "at-2", "uid": "u-2"},
			map[string]any{"accessToken": "at-3", "nickname": "n-3"},
		})
		require.NoError(t, err)
		require.Len(t, accounts, 2)
		require.Equal(t, "at-2", accounts[0].AccessToken)
		require.Equal(t, "n-3", accounts[1].Nickname)
	})

	t.Run("accounts wrapper", func(t *testing.T) {
		accounts, err := parseCodebuddyImportData(map[string]any{
			"accounts": []any{map[string]any{"accessToken": "at-4"}},
		})
		require.NoError(t, err)
		require.Len(t, accounts, 1)
	})

	t.Run("raw json string array", func(t *testing.T) {
		accounts, err := parseCodebuddyImportData(`[{"accessToken":"at-5"}]`)
		require.NoError(t, err)
		require.Len(t, accounts, 1)
		require.Equal(t, "at-5", accounts[0].AccessToken)
	})

	t.Run("raw json string object", func(t *testing.T) {
		accounts, err := parseCodebuddyImportData(`{"accessToken":"at-6"}`)
		require.NoError(t, err)
		require.Len(t, accounts, 1)
	})

	t.Run("missing accessToken errors", func(t *testing.T) {
		_, err := parseCodebuddyImportData([]any{map[string]any{"uid": "u-7"}})
		require.Error(t, err)
	})

	t.Run("empty string errors", func(t *testing.T) {
		_, err := parseCodebuddyImportData("  ")
		require.Error(t, err)
	})

	t.Run("unsupported type errors", func(t *testing.T) {
		_, err := parseCodebuddyImportData(42)
		require.Error(t, err)
	})
}

// TestBuildCodebuddyImportAccountName 账号名生成的优先级链。
func TestBuildCodebuddyImportAccountName(t *testing.T) {
	require.Equal(t, "pool-1", buildCodebuddyImportAccountName("pool", codebuddy.Credentials{}, 0))
	require.Equal(t, "nick-1", buildCodebuddyImportAccountName("", codebuddy.Credentials{Nickname: "nick-1"}, 2))
	require.Equal(t, "u-9", buildCodebuddyImportAccountName("", codebuddy.Credentials{UID: "u-9"}, 2))
	require.Equal(t, "codebuddy-account-3", buildCodebuddyImportAccountName("", codebuddy.Credentials{}, 2))
}

// TestBuildCodebuddyAccountName OAuth 建号的默认名。
func TestBuildCodebuddyAccountName(t *testing.T) {
	require.Equal(t, "我的号", buildCodebuddyAccountName("我的号", nil, nil))
	credentials := &service.CodebuddyLoginCredentials{Nickname: "小明", UID: "u-1"}
	require.Equal(t, "小明", buildCodebuddyAccountName("", credentials, nil))
	require.Equal(t, "u-1", buildCodebuddyAccountName("", &service.CodebuddyLoginCredentials{UID: "u-1"}, nil))
	require.Equal(t, "codebuddy-global", buildCodebuddyAccountName("", &service.CodebuddyLoginCredentials{Region: codebuddy.RegionGlobal}, nil))
	require.Equal(t, "codebuddy-cn", buildCodebuddyAccountName("", &service.CodebuddyLoginCredentials{}, nil))
}

// TestCodebuddyLoginStaticMapping 静态兜底目录为恒等映射且两区不同。
func TestCodebuddyLoginStaticMapping(t *testing.T) {
	cn := codebuddyLoginStaticMapping(codebuddy.RegionCN)
	global := codebuddyLoginStaticMapping(codebuddy.RegionGlobal)
	require.NotEmpty(t, cn)
	require.NotEmpty(t, global)
	for model := range cn {
		require.Equal(t, model, cn[model])
	}
	// 两区静态表必须有差异（模型不互通的前提）。
	differ := false
	for model := range cn {
		if _, ok := global[model]; !ok {
			differ = true
			break
		}
	}
	require.True(t, differ, "CN and Global static model tables should not be identical")
}

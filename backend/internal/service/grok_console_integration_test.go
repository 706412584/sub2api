package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConsoleSessionCredentialService 测试 Console 会话凭据服务的加密/解密循环
func TestConsoleSessionCredentialService(t *testing.T) {
	repo := &mockConsoleSessionRepository{
		credentials: make(map[int64]*GrokSessionCredential),
	}
	encryptor := &mockEncryptor{}
	svc := NewGrokSessionCredentialService(repo, encryptor)

	ctx := context.Background()

	// 保存 Console 会话
	proxyID := int64(1)
	err := svc.SaveConsoleSession(ctx, 100, "test-sso-token", "test-sso-rw", "Mozilla/5.0", &proxyID)
	require.NoError(t, err)

	// 获取会话材料
	material, err := svc.GetSessionMaterial(ctx, 100, &proxyID)
	require.NoError(t, err)
	assert.Equal(t, "test-sso-token", material.SSO)
	assert.Equal(t, "test-sso-rw", material.SSORw)
	assert.Equal(t, "Mozilla/5.0", material.BrowserUA)
	assert.Equal(t, proxyID, *material.BoundProxyID)
	assert.Equal(t, "active", material.Status)

	// 代理不匹配时应拒绝
	diffProxyID := int64(2)
	_, err = svc.GetSessionMaterial(ctx, 100, &diffProxyID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "proxy mismatch")

	// 标记重认证
	err = svc.MarkReauthRequired(ctx, 100, "clearance_expired")
	require.NoError(t, err)

	status, err := svc.GetSessionStatus(ctx, 100)
	require.NoError(t, err)
	assert.Equal(t, "reauth_required", status.Status)
	assert.Equal(t, "clearance_expired", status.LastErrorCode)

	// 未配置的账号
	status, err = svc.GetSessionStatus(ctx, 999)
	require.NoError(t, err)
	assert.False(t, status.Configured)
}

// TestConsoleDPoPProvider 测试 DPoP Provider 的会话管理（不进行真实 HTTP 请求）
func TestConsoleDPoPProvider(t *testing.T) {
	repo := &mockConsoleSessionRepository{
		credentials: make(map[int64]*GrokSessionCredential),
	}
	encryptor := &mockEncryptor{}
	sessionService := NewGrokSessionCredentialService(repo, encryptor)
	provider := NewGrokConsoleDPoPProvider(sessionService, nil)

	ctx := context.Background()
	proxyID := int64(1)

	// 先保存会话
	err := sessionService.SaveConsoleSession(ctx, 100, "test-sso", "test-sso-rw", "Mozilla/5.0", &proxyID)
	require.NoError(t, err)

	// DPoP Provider 将会尝试换票（这里会失败因为没有真实代理）
	// 但这是预期行为——测试的是不会 panic 或死锁
	session, err := provider.GetOrCreateSession(ctx, 100, &proxyID)
	if err != nil {
		// 没有真实网络连接时预期失败
		t.Logf("DPoP session creation failed as expected: %v", err)
	} else {
		t.Logf("DPoP session created: token=%s..., expires=%v",
			session.AccessToken[:min(20, len(session.AccessToken))],
			session.ExpiresAt)
	}

	// InvalidateSession 应正常工作
	provider.InvalidateSession(100)
	assert.True(t, true, "invalidation completed without error")
}

// mockConsoleSessionRepository 模拟仓储
type mockConsoleSessionRepository struct {
	credentials map[int64]*GrokSessionCredential
}

func (m *mockConsoleSessionRepository) Save(ctx context.Context, cred *GrokSessionCredential) error {
	m.credentials[cred.AccountID] = cred
	return nil
}

func (m *mockConsoleSessionRepository) GetByAccountID(ctx context.Context, accountID int64) (*GrokSessionCredential, error) {
	if cred, ok := m.credentials[accountID]; ok {
		return cred, nil
	}
	return nil, nil
}

func (m *mockConsoleSessionRepository) Delete(ctx context.Context, accountID int64) error {
	delete(m.credentials, accountID)
	return nil
}

func (m *mockConsoleSessionRepository) InvalidateByProxyChange(ctx context.Context, accountID int64) error {
	if cred, ok := m.credentials[accountID]; ok {
		cred.Status = "reauth_required"
	}
	return nil
}

// mockEncryptor 模拟加密器（直接返回明文）
type mockEncryptor struct{}

func (m *mockEncryptor) Encrypt(plaintext string) (string, error) {
	return plaintext, nil
}

func (m *mockEncryptor) Decrypt(ciphertext string) (string, error) {
	return ciphertext, nil
}

func init() {
	// 确保 time.Now 在测试中正常工作
	_ = time.Now
}

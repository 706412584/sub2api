package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// grokSessionCredentialRepository 实现 service.GrokSessionCredentialRepository
type grokSessionCredentialRepository struct {
	db *sql.DB
}

// NewGrokSessionCredentialRepository 创建 Grok 会话凭据仓库实例
func NewGrokSessionCredentialRepository(db *sql.DB) service.GrokSessionCredentialRepository {
	return &grokSessionCredentialRepository{db: db}
}

// Save 保存或更新会话凭据（UPSERT）
func (r *grokSessionCredentialRepository) Save(ctx context.Context, session *service.GrokSessionCredential) error {
	query := `
		INSERT INTO grok_session_credentials (
			account_id, source, encrypted_sso, encrypted_sso_rw, encrypted_cf_clearance,
			encrypted_browser_ua, bound_proxy_id, status, last_error_code, last_error_at,
			web_tier, web_terms_version, web_terms_accepted_at, web_birth_date_set_at,
			web_nsfw_enabled_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, NOW(), NOW()
		)
		ON CONFLICT (account_id) DO UPDATE SET
			source = EXCLUDED.source,
			encrypted_sso = EXCLUDED.encrypted_sso,
			encrypted_sso_rw = EXCLUDED.encrypted_sso_rw,
			encrypted_cf_clearance = EXCLUDED.encrypted_cf_clearance,
			encrypted_browser_ua = EXCLUDED.encrypted_browser_ua,
			bound_proxy_id = EXCLUDED.bound_proxy_id,
			status = EXCLUDED.status,
			last_error_code = EXCLUDED.last_error_code,
			last_error_at = EXCLUDED.last_error_at,
			web_tier = EXCLUDED.web_tier,
			web_terms_version = EXCLUDED.web_terms_version,
			web_terms_accepted_at = EXCLUDED.web_terms_accepted_at,
			web_birth_date_set_at = EXCLUDED.web_birth_date_set_at,
			web_nsfw_enabled_at = EXCLUDED.web_nsfw_enabled_at,
			updated_at = NOW()
	`

	_, err := r.db.ExecContext(ctx, query,
		session.AccountID,
		session.Source,
		session.EncryptedSSO,
		session.EncryptedSSOReadWrite,
		session.EncryptedCFClearance,
		session.EncryptedBrowserUA,
		session.BoundProxyID,
		session.Status,
		session.LastErrorCode,
		session.LastErrorAt,
		session.WebTier,
		session.WebTermsVersion,
		session.WebTermsAcceptedAt,
		session.WebBirthDateSetAt,
		session.WebNSFWEnabledAt,
	)
	if err != nil {
		return fmt.Errorf("save grok session credential: %w", err)
	}
	return nil
}

// GetByAccountID 根据账号 ID 获取会话凭据
func (r *grokSessionCredentialRepository) GetByAccountID(ctx context.Context, accountID int64) (*service.GrokSessionCredential, error) {
	query := `
		SELECT account_id, source, encrypted_sso, encrypted_sso_rw, encrypted_cf_clearance,
			encrypted_browser_ua, bound_proxy_id, status, last_error_code, last_error_at,
			web_tier, web_terms_version, web_terms_accepted_at, web_birth_date_set_at,
			web_nsfw_enabled_at, created_at, updated_at
		FROM grok_session_credentials
		WHERE account_id = $1
	`

	session := &service.GrokSessionCredential{}
	err := r.db.QueryRowContext(ctx, query, accountID).Scan(
		&session.AccountID,
		&session.Source,
		&session.EncryptedSSO,
		&session.EncryptedSSOReadWrite,
		&session.EncryptedCFClearance,
		&session.EncryptedBrowserUA,
		&session.BoundProxyID,
		&session.Status,
		&session.LastErrorCode,
		&session.LastErrorAt,
		&session.WebTier,
		&session.WebTermsVersion,
		&session.WebTermsAcceptedAt,
		&session.WebBirthDateSetAt,
		&session.WebNSFWEnabledAt,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get grok session credential: %w", err)
	}
	return session, nil
}

// Delete 删除会话凭据
func (r *grokSessionCredentialRepository) Delete(ctx context.Context, accountID int64) error {
	query := `DELETE FROM grok_session_credentials WHERE account_id = $1`
	_, err := r.db.ExecContext(ctx, query, accountID)
	if err != nil {
		return fmt.Errorf("delete grok session credential: %w", err)
	}
	return nil
}

// InvalidateByProxyChange 代理变更时失效会话（标记需重新认证）
func (r *grokSessionCredentialRepository) InvalidateByProxyChange(ctx context.Context, accountID int64) error {
	query := `
		UPDATE grok_session_credentials
		SET status = 'reauth_required',
			last_error_code = 'proxy_changed',
			last_error_at = NOW(),
			updated_at = NOW()
		WHERE account_id = $1
	`
	_, err := r.db.ExecContext(ctx, query, accountID)
	if err != nil {
		return fmt.Errorf("invalidate grok session by proxy change: %w", err)
	}
	return nil
}

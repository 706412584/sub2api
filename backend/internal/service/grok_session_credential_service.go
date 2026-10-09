package service

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// GrokSessionCredentialRepository 定义会话凭据存储接口（避免循环导入）
type GrokSessionCredentialRepository interface {
	Save(ctx context.Context, session *GrokSessionCredential) error
	GetByAccountID(ctx context.Context, accountID int64) (*GrokSessionCredential, error)
	Delete(ctx context.Context, accountID int64) error
	InvalidateByProxyChange(ctx context.Context, accountID int64) error
}

// GrokSessionCredential 表示 Grok Web/Console 的加密会话凭据
type GrokSessionCredential struct {
	AccountID             int64
	Source                string
	EncryptedSSO          string
	EncryptedSSOReadWrite *string
	EncryptedCFClearance  *string
	EncryptedBrowserUA    string
	BoundProxyID          *int64
	Status                string
	LastErrorCode         *string
	LastErrorAt           *time.Time
	WebTier               *string
	WebTermsVersion       *string
	WebTermsAcceptedAt    *time.Time
	WebBirthDateSetAt     *time.Time
	WebNSFWEnabledAt      *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// ErrGrokSessionUnavailable 会话服务未注入时的错误
var ErrGrokSessionUnavailable = fmt.Errorf("grok session credential service is not available")

// GrokSessionCredentialService Grok 会话凭据服务接口
type GrokSessionCredentialService interface {
	SaveConsoleSession(ctx context.Context, accountID int64, sso, ssoRW, ua string, proxyID *int64) error
	SaveWebSession(ctx context.Context, accountID int64, sso, ssoRW, cfClearance, ua string, proxyID *int64) error
	GetSessionMaterial(ctx context.Context, accountID int64, currentProxyID *int64) (*DecryptedSessionMaterial, error)
	MarkReauthRequired(ctx context.Context, accountID int64, errorCode string) error
	DeleteSession(ctx context.Context, accountID int64) error
	GetSessionStatus(ctx context.Context, accountID int64) (*SessionStatusDTO, error)
}

// DecryptedSessionMaterial 解密后的会话材料
type DecryptedSessionMaterial struct {
	SSO          string
	SSORw        string
	CFClearance  string
	BrowserUA    string
	BoundProxyID *int64
	Status       string
	WebTier      string
	Source       string
}

// SessionStatusDTO 会话状态 DTO（仅非敏感字段）
type SessionStatusDTO struct {
	Configured     bool       `json:"configured"`
	Status         string     `json:"status,omitempty"`
	BoundProxyID   *int64     `json:"bound_proxy_id,omitempty"`
	UpdatedAt      *time.Time `json:"updated_at,omitempty"`
	WebTier        string     `json:"web_tier,omitempty"`
	LastErrorCode  string     `json:"last_error_code,omitempty"`
	LastErrorAt    *time.Time `json:"last_error_at,omitempty"`
	HasSSO         bool       `json:"has_sso"`
	HasCFClearance bool       `json:"has_cf_clearance"`
	HasBrowserUA   bool       `json:"has_browser_ua"`
}

type grokSessionCredentialService struct {
	repo      GrokSessionCredentialRepository
	encryptor SecretEncryptor
}

// NewGrokSessionCredentialService 创建会话凭据服务实例
func NewGrokSessionCredentialService(
	repo GrokSessionCredentialRepository,
	encryptor SecretEncryptor,
) GrokSessionCredentialService {
	return &grokSessionCredentialService{
		repo:      repo,
		encryptor: encryptor,
	}
}

// SaveConsoleSession 保存 Console 会话（SSO + UA + 代理）
func (s *grokSessionCredentialService) SaveConsoleSession(
	ctx context.Context,
	accountID int64,
	sso, ssoRW, ua string,
	proxyID *int64,
) error {
	sso = strings.TrimSpace(sso)
	ssoRW = strings.TrimSpace(ssoRW)
	ua = strings.TrimSpace(ua)

	if sso == "" {
		return fmt.Errorf("sso token is required for console session")
	}
	if ua == "" {
		return fmt.Errorf("browser user agent is required for console session")
	}
	if proxyID == nil {
		return fmt.Errorf("proxy is required for console session")
	}

	if len(sso) > 8192 {
		return fmt.Errorf("sso token exceeds maximum length")
	}
	if len(ua) > 1024 {
		return fmt.Errorf("user agent exceeds maximum length")
	}

	encryptedSSO, err := s.encryptor.Encrypt(sso)
	if err != nil {
		return fmt.Errorf("encrypt sso: %w", err)
	}

	var encryptedSSORW *string
	if ssoRW != "" {
		enc, err := s.encryptor.Encrypt(ssoRW)
		if err != nil {
			return fmt.Errorf("encrypt sso_rw: %w", err)
		}
		encryptedSSORW = &enc
	}

	encryptedUA, err := s.encryptor.Encrypt(ua)
	if err != nil {
		return fmt.Errorf("encrypt user agent: %w", err)
	}

	session := &GrokSessionCredential{
		AccountID:             accountID,
		Source:                "console",
		EncryptedSSO:          encryptedSSO,
		EncryptedSSOReadWrite: encryptedSSORW,
		EncryptedBrowserUA:    encryptedUA,
		BoundProxyID:          proxyID,
		Status:                "active",
	}

	return s.repo.Save(ctx, session)
}

// SaveWebSession 保存 Web 会话（SSO + cf_clearance + UA + 代理）
func (s *grokSessionCredentialService) SaveWebSession(
	ctx context.Context,
	accountID int64,
	sso, ssoRW, cfClearance, ua string,
	proxyID *int64,
) error {
	sso = strings.TrimSpace(sso)
	ssoRW = strings.TrimSpace(ssoRW)
	cfClearance = strings.TrimSpace(cfClearance)
	ua = strings.TrimSpace(ua)

	if sso == "" {
		return fmt.Errorf("sso token is required for web session")
	}
	if cfClearance == "" {
		return fmt.Errorf("cf_clearance is required for web session")
	}
	if ua == "" {
		return fmt.Errorf("browser user agent is required for web session")
	}
	if proxyID == nil {
		return fmt.Errorf("proxy is required for web session")
	}

	if len(sso) > 8192 {
		return fmt.Errorf("sso token exceeds maximum length")
	}
	if len(cfClearance) > 8192 {
		return fmt.Errorf("cf_clearance exceeds maximum length")
	}
	if len(ua) > 1024 {
		return fmt.Errorf("user agent exceeds maximum length")
	}

	encryptedSSO, err := s.encryptor.Encrypt(sso)
	if err != nil {
		return fmt.Errorf("encrypt sso: %w", err)
	}

	var encryptedSSORW *string
	if ssoRW != "" {
		enc, err := s.encryptor.Encrypt(ssoRW)
		if err != nil {
			return fmt.Errorf("encrypt sso_rw: %w", err)
		}
		encryptedSSORW = &enc
	}

	encryptedCFClearance, err := s.encryptor.Encrypt(cfClearance)
	if err != nil {
		return fmt.Errorf("encrypt cf_clearance: %w", err)
	}

	encryptedUA, err := s.encryptor.Encrypt(ua)
	if err != nil {
		return fmt.Errorf("encrypt user agent: %w", err)
	}

	session := &GrokSessionCredential{
		AccountID:             accountID,
		Source:                "web",
		EncryptedSSO:          encryptedSSO,
		EncryptedSSOReadWrite: encryptedSSORW,
		EncryptedCFClearance:  &encryptedCFClearance,
		EncryptedBrowserUA:    encryptedUA,
		BoundProxyID:          proxyID,
		Status:                "active",
	}

	return s.repo.Save(ctx, session)
}

// GetSessionMaterial 获取解密后的会话材料
func (s *grokSessionCredentialService) GetSessionMaterial(
	ctx context.Context,
	accountID int64,
	currentProxyID *int64,
) (*DecryptedSessionMaterial, error) {
	session, err := s.repo.GetByAccountID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("session not found")
	}

	// 代理校验
	if session.BoundProxyID != nil && currentProxyID != nil {
		if *session.BoundProxyID != *currentProxyID {
			return nil, fmt.Errorf("session proxy mismatch: bound=%d current=%d", *session.BoundProxyID, *currentProxyID)
		}
	}

	if session.Status != "active" {
		return nil, fmt.Errorf("session status is %s", session.Status)
	}

	sso, err := s.encryptor.Decrypt(session.EncryptedSSO)
	if err != nil {
		return nil, fmt.Errorf("decrypt sso: %w", err)
	}

	ssoRW := sso
	if session.EncryptedSSOReadWrite != nil {
		decrypted, err := s.encryptor.Decrypt(*session.EncryptedSSOReadWrite)
		if err != nil {
			return nil, fmt.Errorf("decrypt sso_rw: %w", err)
		}
		ssoRW = decrypted
	}

	ua, err := s.encryptor.Decrypt(session.EncryptedBrowserUA)
	if err != nil {
		return nil, fmt.Errorf("decrypt user agent: %w", err)
	}

	var cfClearance string
	if session.EncryptedCFClearance != nil {
		decrypted, err := s.encryptor.Decrypt(*session.EncryptedCFClearance)
		if err != nil {
			return nil, fmt.Errorf("decrypt cf_clearance: %w", err)
		}
		cfClearance = decrypted
	}

	webTier := ""
	if session.WebTier != nil {
		webTier = *session.WebTier
	}

	return &DecryptedSessionMaterial{
		SSO:          sso,
		SSORw:        ssoRW,
		CFClearance:  cfClearance,
		BrowserUA:    ua,
		BoundProxyID: session.BoundProxyID,
		Status:       session.Status,
		WebTier:      webTier,
		Source:       session.Source,
	}, nil
}

// MarkReauthRequired 标记需要重新认证
func (s *grokSessionCredentialService) MarkReauthRequired(ctx context.Context, accountID int64, errorCode string) error {
	session, err := s.repo.GetByAccountID(ctx, accountID)
	if err != nil {
		return err
	}
	if session == nil {
		return nil
	}

	now := time.Now()
	session.Status = "reauth_required"
	session.LastErrorCode = &errorCode
	session.LastErrorAt = &now

	return s.repo.Save(ctx, session)
}

// DeleteSession 删除会话
func (s *grokSessionCredentialService) DeleteSession(ctx context.Context, accountID int64) error {
	return s.repo.Delete(ctx, accountID)
}

// GetSessionStatus 获取会话状态（仅非敏感字段）
func (s *grokSessionCredentialService) GetSessionStatus(ctx context.Context, accountID int64) (*SessionStatusDTO, error) {
	session, err := s.repo.GetByAccountID(ctx, accountID)
	if err != nil {
		return nil, err
	}

	if session == nil {
		return &SessionStatusDTO{
			Configured: false,
		}, nil
	}

	dto := &SessionStatusDTO{
		Configured:     true,
		Status:         session.Status,
		BoundProxyID:   session.BoundProxyID,
		UpdatedAt:      &session.UpdatedAt,
		HasSSO:         session.EncryptedSSO != "",
		HasBrowserUA:   session.EncryptedBrowserUA != "",
		HasCFClearance: session.EncryptedCFClearance != nil && *session.EncryptedCFClearance != "",
	}

	if session.WebTier != nil {
		dto.WebTier = *session.WebTier
	}
	if session.LastErrorCode != nil {
		dto.LastErrorCode = *session.LastErrorCode
	}
	if session.LastErrorAt != nil {
		dto.LastErrorAt = session.LastErrorAt
	}

	return dto, nil
}

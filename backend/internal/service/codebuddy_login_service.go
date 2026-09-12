package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codebuddy"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

	"github.com/google/uuid"
)

// CodeBuddy OAuth 登录（服务端签发 state，无 PKCE）。
//
// 流程来源 workbuddy2api cmd/login/main.go：
//
//	start → POST /v2/plugin/auth/state?platform=CLI 拿 state+authUrl，用户浏览器授权
//	poll  → GET  /v2/plugin/auth/token?state= 轮询 token（pending 时业务 code != 0）
//	        成功后 GET /v2/plugin/login/account?state= 拿 uid/nickname（带 Bearer）
//
// 会话状态机照搬 kiro_device_flow_service.go 的内存 session map + 前端轮询模型。
type CodebuddyLoginService struct {
	httpClient *http.Client
	mu         sync.Mutex
	sessions   map[string]*codebuddyLoginSession
}

type CodebuddyLoginStartResult struct {
	SessionID string `json:"session_id"`
	AuthURL   string `json:"auth_url"`
	Region    string `json:"region"`
	ExpiresIn int64  `json:"expires_in"`
}

type CodebuddyLoginPollResult struct {
	Status    string `json:"status"` // pending | authorized
	ExpiresIn int64  `json:"expires_in,omitempty"`
	Nickname  string `json:"nickname,omitempty"`
}

type CodebuddyLoginCredentials struct {
	Region       codebuddy.Region
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
	Domain       string
	UID          string
	EnterpriseID string
	Nickname     string
}

type codebuddyLoginSession struct {
	Region         codebuddy.Region
	State          string
	AuthURL        string
	ExpiresIn      int64
	CreatedAt      time.Time
	AccessToken    string
	RefreshToken   string
	TokenExpiresIn int64
	TokenExpiresAt int64
	Domain         string
	UID            string
	EnterpriseID   string
	Nickname       string
	AuthorizedAt   *time.Time
}

const (
	codebuddyLoginSessionTTL = 10 * time.Minute
	codebuddyLoginReadLimit  = 1 << 20
)

func NewCodebuddyLoginService() *CodebuddyLoginService {
	return &CodebuddyLoginService{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		sessions:   make(map[string]*codebuddyLoginSession),
	}
}

// normalizeCodebuddyLoginRegion 归一化前端传入的区域参数；缺省 CN。
func normalizeCodebuddyLoginRegion(region string) codebuddy.Region {
	if strings.EqualFold(strings.TrimSpace(region), string(codebuddy.RegionGlobal)) {
		return codebuddy.RegionGlobal
	}
	return codebuddy.RegionCN
}

// Start 申请 OAuth state 并返回授权 URL。
func (s *CodebuddyLoginService) Start(ctx context.Context, region string) (*CodebuddyLoginStartResult, error) {
	r := normalizeCodebuddyLoginRegion(region)
	req, err := codebuddy.BuildAuthStateRequest(r, codebuddy.AuthFlowOptions{})
	if err != nil {
		return nil, infraerrors.InternalServer("CODEBUDDY_LOGIN_STATE_REQUEST_BUILD_FAILED", "failed to build codebuddy auth state request").WithCause(err)
	}
	req = req.WithContext(ctx)
	body, status, err := s.doRequest(req)
	if err != nil {
		return nil, infraerrors.BadRequest("CODEBUDDY_LOGIN_STATE_REQUEST_FAILED", "failed to reach codebuddy auth state endpoint").WithCause(err)
	}
	data, err := codebuddy.ParseEnvelope(status, body)
	if err != nil {
		return nil, infraerrors.BadRequest("CODEBUDDY_LOGIN_STATE_FAILED", fmt.Sprintf("codebuddy auth state returned error: %v", err))
	}
	var st struct {
		State   string `json:"state"`
		AuthURL string `json:"authUrl"`
	}
	if err := json.Unmarshal(data, &st); err != nil || strings.TrimSpace(st.State) == "" {
		return nil, infraerrors.InternalServer("CODEBUDDY_LOGIN_STATE_PARSE_FAILED", "codebuddy auth state response is invalid")
	}
	if strings.TrimSpace(st.AuthURL) == "" {
		return nil, infraerrors.InternalServer("CODEBUDDY_LOGIN_STATE_NO_URL", "codebuddy auth state response missing authUrl")
	}

	sessionID := uuid.NewString()
	now := time.Now()
	s.mu.Lock()
	s.cleanupExpiredLocked(now)
	s.sessions[sessionID] = &codebuddyLoginSession{
		Region:    r,
		State:     strings.TrimSpace(st.State),
		AuthURL:   strings.TrimSpace(st.AuthURL),
		ExpiresIn: int64(codebuddyLoginSessionTTL / time.Second),
		CreatedAt: now,
	}
	s.mu.Unlock()

	return &CodebuddyLoginStartResult{
		SessionID: sessionID,
		AuthURL:   strings.TrimSpace(st.AuthURL),
		Region:    string(r),
		ExpiresIn: int64(codebuddyLoginSessionTTL / time.Second),
	}, nil
}

// Poll 轮询登录状态；完成时补拉 uid/nickname 并缓存进会话。
func (s *CodebuddyLoginService) Poll(ctx context.Context, sessionID string) (*CodebuddyLoginPollResult, error) {
	session, err := s.getSession(sessionID)
	if err != nil {
		return nil, err
	}
	if session.isExpired(time.Now()) {
		s.deleteSession(sessionID)
		return nil, infraerrors.BadRequest("CODEBUDDY_LOGIN_SESSION_EXPIRED", "codebuddy login session has expired")
	}

	if strings.TrimSpace(session.AccessToken) == "" {
		authorized, pollErr := s.pollToken(ctx, sessionID, session)
		if pollErr != nil {
			return nil, pollErr
		}
		if !authorized {
			return &CodebuddyLoginPollResult{
				Status:    "pending",
				ExpiresIn: session.remainingTTL(),
			}, nil
		}
		session, err = s.getSession(sessionID)
		if err != nil {
			return nil, err
		}
	}

	return &CodebuddyLoginPollResult{
		Status:    "authorized",
		ExpiresIn: session.remainingTTL(),
		Nickname:  session.Nickname,
	}, nil
}

// pollToken 调用 auth/token 端点一次。pending（业务 code != 0 的"login ing"）
// 返回 (false, nil)；完成则写入 token 并拉取账号信息。
func (s *CodebuddyLoginService) pollToken(ctx context.Context, sessionID string, session *codebuddyLoginSession) (bool, error) {
	req, err := codebuddy.BuildAuthTokenRequest(session.Region, session.State, codebuddy.AuthFlowOptions{})
	if err != nil {
		return false, infraerrors.InternalServer("CODEBUDDY_LOGIN_TOKEN_REQUEST_BUILD_FAILED", "failed to build codebuddy token request").WithCause(err)
	}
	req = req.WithContext(ctx)
	body, status, err := s.doRequest(req)
	if err != nil {
		return false, infraerrors.BadRequest("CODEBUDDY_LOGIN_TOKEN_REQUEST_FAILED", "failed to reach codebuddy token endpoint").WithCause(err)
	}
	data, parseErr := codebuddy.ParseEnvelope(status, body)
	if parseErr != nil || len(data) == 0 {
		// pending：token 端点在登录未完成时业务 code 非 0（"login ing"），属正常轮询状态。
		return false, nil
	}
	var tok struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		Domain       string `json:"domain"`
	}
	if json.Unmarshal(data, &tok) != nil || strings.TrimSpace(tok.AccessToken) == "" {
		return false, nil
	}

	now := time.Now()
	uid, enterpriseID, nickname := s.fetchLoginAccount(ctx, session.Region, session.State, tok.AccessToken)

	s.mu.Lock()
	if current, ok := s.sessions[sessionID]; ok {
		current.AccessToken = strings.TrimSpace(tok.AccessToken)
		current.RefreshToken = strings.TrimSpace(tok.RefreshToken)
		current.Domain = strings.TrimSpace(tok.Domain)
		current.TokenExpiresIn = tok.ExpiresIn
		if tok.ExpiresIn > 0 {
			current.TokenExpiresAt = now.Unix() + tok.ExpiresIn
		}
		if uid != "" {
			current.UID = uid
		}
		if enterpriseID != "" {
			current.EnterpriseID = enterpriseID
		}
		if nickname != "" {
			current.Nickname = nickname
		}
		current.AuthorizedAt = &now
	}
	s.mu.Unlock()
	return true, nil
}

// fetchLoginAccount 拿 uid/nickname（带 Bearer）；失败不阻断授权结果。
func (s *CodebuddyLoginService) fetchLoginAccount(ctx context.Context, region codebuddy.Region, state, accessToken string) (uid, enterpriseID, nickname string) {
	req, err := codebuddy.BuildLoginAccountRequest(region, state, codebuddy.AuthFlowOptions{})
	if err != nil {
		return "", "", ""
	}
	req = req.WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	body, status, err := s.doRequest(req)
	if err != nil {
		return "", "", ""
	}
	data, parseErr := codebuddy.ParseEnvelope(status, body)
	if parseErr != nil {
		return "", "", ""
	}
	var acct struct {
		UID          string `json:"uid"`
		EnterpriseID string `json:"enterpriseId"`
		Nickname     string `json:"nickname"`
	}
	if json.Unmarshal(data, &acct) != nil {
		return "", "", ""
	}
	return strings.TrimSpace(acct.UID), strings.TrimSpace(acct.EnterpriseID), strings.TrimSpace(acct.Nickname)
}

// Credentials 返回已完成授权的会话凭证。
func (s *CodebuddyLoginService) Credentials(sessionID string) (*CodebuddyLoginCredentials, error) {
	session, err := s.getSession(sessionID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(session.AccessToken) == "" {
		return nil, infraerrors.BadRequest("CODEBUDDY_LOGIN_NOT_AUTHORIZED", "codebuddy login session is not authorized yet")
	}
	return &CodebuddyLoginCredentials{
		Region:       session.Region,
		AccessToken:  session.AccessToken,
		RefreshToken: session.RefreshToken,
		ExpiresAt:    session.TokenExpiresAt,
		Domain:       session.Domain,
		UID:          session.UID,
		EnterpriseID: session.EnterpriseID,
		Nickname:     session.Nickname,
	}, nil
}

func (s *CodebuddyLoginService) doRequest(req *http.Request) ([]byte, int, error) {
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, codebuddyLoginReadLimit))
	if readErr != nil {
		return nil, resp.StatusCode, readErr
	}
	return body, resp.StatusCode, nil
}

func (s *CodebuddyLoginService) getSession(sessionID string) (*codebuddyLoginSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupExpiredLocked(time.Now())
	session, ok := s.sessions[strings.TrimSpace(sessionID)]
	if !ok {
		return nil, infraerrors.BadRequest("CODEBUDDY_LOGIN_SESSION_NOT_FOUND", "codebuddy login session not found")
	}
	return cloneCodebuddyLoginSession(session), nil
}

func (s *CodebuddyLoginService) deleteSession(sessionID string) {
	s.mu.Lock()
	delete(s.sessions, strings.TrimSpace(sessionID))
	s.mu.Unlock()
}

func (s *CodebuddyLoginService) cleanupExpiredLocked(now time.Time) {
	for id, session := range s.sessions {
		if session.isExpired(now) {
			delete(s.sessions, id)
		}
	}
}

func (s *codebuddyLoginSession) isExpired(now time.Time) bool {
	return now.After(s.CreatedAt.Add(time.Duration(s.ExpiresIn) * time.Second))
}

func (s *codebuddyLoginSession) remainingTTL() int64 {
	remaining := s.CreatedAt.Add(time.Duration(s.ExpiresIn)*time.Second).Unix() - time.Now().Unix()
	if remaining < 0 {
		return 0
	}
	return remaining
}

func cloneCodebuddyLoginSession(src *codebuddyLoginSession) *codebuddyLoginSession {
	dst := *src
	if src.AuthorizedAt != nil {
		authorizedAt := *src.AuthorizedAt
		dst.AuthorizedAt = &authorizedAt
	}
	return &dst
}

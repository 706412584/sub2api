package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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

	// 拿到 uid 后补激活（仅 Global）。失败不阻断：token 已到手，凭证照常产出，
	// 只是该账号 chat 会回 429 code=14017，需要重新登录或人工处理。
	if uid != "" {
		s.activateAccount(ctx, session.Region, tok.AccessToken, tok.Domain, uid, enterpriseID)
	}

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

// activateAccount 补跑 Global 账号激活三步（注册地 → registerCloud → trial）。
//
// 为什么必须做：登录只拿 token，账号仍是「未激活」，chat 会回 429 code=14017
// （"The trial version is not yet activated"）。网页登录会多做这三步，网关不做就
// 只能拿到一个用不了的账号。顺序不可颠倒：跳过提交注册地直接调 registerCloud 会回
// {"code":500,"msg":"register failed:register region required"}。
//
// 仅 Global：CN 的同名路径语义不同（get-user-area-info 返回 WAF 拦截 HTML 而非
// JSON；trial 对已激活账号回 14051）。没有 CN 未激活账号可供实测，故不猜。
//
// 全程尽力而为：任一步失败只记日志，不阻断登录结果（token 已到手，凭证仍可用）。
func (s *CodebuddyLoginService) activateAccount(ctx context.Context, region codebuddy.Region, accessToken, domain, uid, enterpriseID string) {
	if region != codebuddy.RegionGlobal {
		return
	}
	creds := codebuddy.Credentials{
		AccessToken:  accessToken,
		Domain:       domain,
		UID:          uid,
		EnterpriseID: enterpriseID,
	}

	// 1. 取检测到的注册地（IOS2）。data 是嵌套的 JSON 字符串，要解两层。
	//    取不到就退回 SG —— intl 账号的常见归属，且第 2 步只要求「有个区域」。
	countryName, countryCode, countryFullName := "SG", "65", "Singapore"
	if req, err := codebuddy.BuildUserAreaInfoRequest(creds, region, codebuddy.EndpointOptions{}); err == nil {
		if data, ok := s.doActivateRequest(ctx, req); ok {
			var outer string
			if json.Unmarshal(data, &outer) == nil {
				var inner struct {
					Data struct {
						IOS2   string `json:"IOS2"`
						EnName string `json:"enName"`
						Code   string `json:"code"`
					} `json:"data"`
				}
				if json.Unmarshal([]byte(outer), &inner) == nil && inner.Data.IOS2 != "" {
					countryName = inner.Data.IOS2
					if inner.Data.EnName != "" {
						countryFullName = inner.Data.EnName
					}
					if inner.Data.Code != "" {
						countryCode = inner.Data.Code
					}
				}
			}
		}
	}

	// 2. 提交注册地（areaInfoComplete 翻 true）。
	if req, err := codebuddy.BuildSubmitAreaRequest(creds, region, countryCode, countryFullName, countryName, codebuddy.EndpointOptions{}); err == nil {
		if _, ok := s.doActivateRequest(ctx, req); !ok {
			slog.Warn("codebuddy_login.activate_submit_area_failed", "uid", uid)
			return
		}
	}

	// 3. registerCloud。成功时返回 code=200 而非 0，会被 ParseEnvelope 当错误 —— 只记不报。
	if req, err := codebuddy.BuildRegisterCloudRequest(creds, region, uid, codebuddy.EndpointOptions{}); err == nil {
		if _, ok := s.doActivateRequest(ctx, req); !ok {
			slog.Info("codebuddy_login.activate_register_cloud_nonzero", "uid", uid)
		}
	}

	// 4. trial —— 翻转 14017 的那一步。14051「has applied trial」是已领过的正常结果。
	if req, err := codebuddy.BuildTrialRequest(creds, region, codebuddy.EndpointOptions{}); err == nil {
		if _, ok := s.doActivateRequest(ctx, req); ok {
			slog.Info("codebuddy_login.activate_done", "uid", uid, "country", countryName)
		} else {
			slog.Warn("codebuddy_login.activate_trial_failed", "uid", uid)
		}
	}
}

// doActivateRequest 执行一次激活请求并解析信封；成功返回 data，失败返回 false。
// 14051（试用已领过）视作成功：账号本就是激活态，无需再激活。
func (s *CodebuddyLoginService) doActivateRequest(ctx context.Context, req *http.Request) ([]byte, bool) {
	body, status, err := s.doRequest(req.WithContext(ctx))
	if err != nil {
		return nil, false
	}
	data, parseErr := codebuddy.ParseEnvelope(status, body)
	if parseErr != nil {
		if codebuddy.ErrorCodeOf(parseErr) == codebuddy.CodeTrialAlreadyApplied {
			return nil, true
		}
		return nil, false
	}
	return data, true
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

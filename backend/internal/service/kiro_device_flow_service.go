package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	kiroBuilderIDStartURL   = "https://view.awsapps.com/start"
	kiroBuilderIDClientName = "sub2api Kiro Login"
)

var (
	kiroBuilderIDScopes = []string{
		"codewhisperer:analysis",
		"codewhisperer:completions",
		"codewhisperer:conversations",
		"codewhisperer:taskassist",
		"codewhisperer:transformations",
	}
	kiroBuilderIDRegionPattern = regexp.MustCompile(`^[a-z]{2}(?:-gov)?-[a-z]+-\d$`)
)

type KiroBuilderIDDeviceFlowService struct {
	httpClient *http.Client
	mu         sync.RWMutex
	sessions   map[string]*kiroBuilderIDSession
}

type KiroBuilderIDStartResult struct {
	SessionID               string `json:"session_id"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int64  `json:"expires_in"`
	Interval                int64  `json:"interval"`
	Region                  string `json:"region"`
}

type KiroBuilderIDPollResult struct {
	Status       string `json:"status"`
	Interval     int64  `json:"interval,omitempty"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
	AuthorizedAt int64  `json:"authorized_at,omitempty"`
}

type KiroBuilderIDCredentials struct {
	Region       string
	ClientID     string
	ClientSecret string
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
	ExpiresAt    int64
	ProfileArn   string
	Scopes       []string
}

type kiroBuilderIDSession struct {
	Region                  string
	ClientID                string
	ClientSecret            string
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               int64
	Interval                int64
	CreatedAt               time.Time
	AuthorizedAt            *time.Time
	AccessToken             string
	RefreshToken            string
	TokenExpiresIn          int64
	TokenExpiresAt          int64
	ProfileArn              string
	Scopes                  []string
}

type kiroBuilderIDRegisterRequest struct {
	ClientName string   `json:"clientName"`
	ClientType string   `json:"clientType"`
	Scopes     []string `json:"scopes"`
	GrantTypes []string `json:"grantTypes"`
	IssuerURL  string   `json:"issuerUrl"`
}

type kiroBuilderIDRegisterResponse struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type kiroBuilderIDDeviceAuthorizationRequest struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	StartURL     string `json:"startUrl"`
}

type kiroBuilderIDDeviceAuthorizationResponse struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationUri"`
	VerificationURIComplete string `json:"verificationUriComplete"`
	ExpiresIn               int64  `json:"expiresIn"`
	Interval                int64  `json:"interval"`
}

type kiroBuilderIDTokenRequest struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	GrantType    string `json:"grantType"`
	DeviceCode   string `json:"deviceCode,omitempty"`
}

type kiroBuilderIDTokenSuccessResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int64  `json:"expiresIn"`
	ProfileArn   string `json:"profileArn"`
}

type kiroBuilderIDTokenErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func NewKiroBuilderIDDeviceFlowService() *KiroBuilderIDDeviceFlowService {
	return &KiroBuilderIDDeviceFlowService{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		sessions:   make(map[string]*kiroBuilderIDSession),
	}
}

func (s *KiroBuilderIDDeviceFlowService) Start(ctx context.Context, region string) (*KiroBuilderIDStartResult, error) {
	region = normalizeKiroBuilderIDRegion(region)
	registerResp := &kiroBuilderIDRegisterResponse{}
	if err := s.postJSON(ctx, kiroBuilderIDOIDCURL(region, "/client/register"), kiroBuilderIDRegisterRequest{
		ClientName: kiroBuilderIDClientName,
		ClientType: "public",
		Scopes:     append([]string(nil), kiroBuilderIDScopes...),
		GrantTypes: []string{"urn:ietf:params:oauth:grant-type:device_code", "refresh_token"},
		IssuerURL:  kiroBuilderIDStartURL,
	}, registerResp); err != nil {
		return nil, err
	}
	if strings.TrimSpace(registerResp.ClientID) == "" || strings.TrimSpace(registerResp.ClientSecret) == "" {
		return nil, infraerrors.InternalServer("KIRO_BUILDER_ID_REGISTER_INVALID", "builder id registration response is invalid")
	}

	deviceResp := &kiroBuilderIDDeviceAuthorizationResponse{}
	if err := s.postJSON(ctx, kiroBuilderIDOIDCURL(region, "/device_authorization"), kiroBuilderIDDeviceAuthorizationRequest{
		ClientID:     registerResp.ClientID,
		ClientSecret: registerResp.ClientSecret,
		StartURL:     kiroBuilderIDStartURL,
	}, deviceResp); err != nil {
		return nil, err
	}
	if deviceResp.Interval <= 0 {
		deviceResp.Interval = 5
	}
	if deviceResp.ExpiresIn <= 0 {
		deviceResp.ExpiresIn = 600
	}

	sessionID := uuid.NewString()
	s.mu.Lock()
	s.cleanupExpiredLocked(time.Now())
	s.sessions[sessionID] = &kiroBuilderIDSession{
		Region:                  region,
		ClientID:                strings.TrimSpace(registerResp.ClientID),
		ClientSecret:            strings.TrimSpace(registerResp.ClientSecret),
		DeviceCode:              strings.TrimSpace(deviceResp.DeviceCode),
		UserCode:                strings.TrimSpace(deviceResp.UserCode),
		VerificationURI:         strings.TrimSpace(deviceResp.VerificationURI),
		VerificationURIComplete: strings.TrimSpace(deviceResp.VerificationURIComplete),
		ExpiresIn:               deviceResp.ExpiresIn,
		Interval:                deviceResp.Interval,
		CreatedAt:               time.Now(),
		Scopes:                  append([]string(nil), kiroBuilderIDScopes...),
	}
	s.mu.Unlock()

	return &KiroBuilderIDStartResult{
		SessionID:               sessionID,
		UserCode:                strings.TrimSpace(deviceResp.UserCode),
		VerificationURI:         strings.TrimSpace(deviceResp.VerificationURI),
		VerificationURIComplete: strings.TrimSpace(deviceResp.VerificationURIComplete),
		ExpiresIn:               deviceResp.ExpiresIn,
		Interval:                deviceResp.Interval,
		Region:                  region,
	}, nil
}

func (s *KiroBuilderIDDeviceFlowService) Poll(ctx context.Context, sessionID string) (*KiroBuilderIDPollResult, error) {
	session, err := s.getSession(sessionID)
	if err != nil {
		return nil, err
	}
	if session.isExpired(time.Now()) {
		s.deleteSession(sessionID)
		return nil, infraerrors.BadRequest("KIRO_BUILDER_ID_SESSION_EXPIRED", "builder id device flow session has expired")
	}
	if session.AccessToken != "" {
		return &KiroBuilderIDPollResult{
			Status:       "authorized",
			Interval:     session.Interval,
			ExpiresIn:    remainingSeconds(session.TokenExpiresAt),
			AuthorizedAt: session.authorizedAtUnix(),
		}, nil
	}

	respBody, statusCode, err := s.postJSONRaw(ctx, kiroBuilderIDOIDCURL(session.Region, "/token"), kiroBuilderIDTokenRequest{
		ClientID:     session.ClientID,
		ClientSecret: session.ClientSecret,
		GrantType:    "urn:ietf:params:oauth:grant-type:device_code",
		DeviceCode:   session.DeviceCode,
	})
	if err != nil {
		return nil, err
	}
	if statusCode == http.StatusOK {
		var success kiroBuilderIDTokenSuccessResponse
		if err := json.Unmarshal(respBody, &success); err != nil {
			return nil, infraerrors.InternalServer("KIRO_BUILDER_ID_TOKEN_PARSE_FAILED", "failed to parse builder id token response").WithCause(err)
		}
		now := time.Now()
		expiresIn := success.ExpiresIn
		if expiresIn <= 0 {
			expiresIn = 3600
		}
		s.mu.Lock()
		if current, ok := s.sessions[sessionID]; ok {
			current.AccessToken = strings.TrimSpace(success.AccessToken)
			current.RefreshToken = strings.TrimSpace(success.RefreshToken)
			current.TokenExpiresIn = expiresIn
			current.TokenExpiresAt = now.Unix() + expiresIn
			current.ProfileArn = strings.TrimSpace(success.ProfileArn)
			current.AuthorizedAt = &now
			session = cloneKiroBuilderIDSession(current)
		}
		s.mu.Unlock()
		return &KiroBuilderIDPollResult{
			Status:       "authorized",
			Interval:     session.Interval,
			ExpiresIn:    expiresIn,
			AuthorizedAt: now.Unix(),
		}, nil
	}

	var failure kiroBuilderIDTokenErrorResponse
	_ = json.Unmarshal(respBody, &failure)
	switch strings.TrimSpace(failure.Error) {
	case "authorization_pending":
		return &KiroBuilderIDPollResult{Status: "pending", Interval: session.Interval, ExpiresIn: session.remainingSessionTTL()}, nil
	case "slow_down":
		s.mu.Lock()
		if current, ok := s.sessions[sessionID]; ok {
			current.Interval++
			session = cloneKiroBuilderIDSession(current)
		}
		s.mu.Unlock()
		return &KiroBuilderIDPollResult{Status: "pending", Interval: session.Interval, ExpiresIn: session.remainingSessionTTL()}, nil
	case "expired_token":
		s.deleteSession(sessionID)
		return nil, infraerrors.BadRequest("KIRO_BUILDER_ID_TOKEN_EXPIRED", "builder id authorization has expired")
	case "access_denied":
		s.deleteSession(sessionID)
		return nil, infraerrors.BadRequest("KIRO_BUILDER_ID_ACCESS_DENIED", "builder id authorization was denied")
	default:
		message := strings.TrimSpace(failure.ErrorDescription)
		if message == "" {
			message = "builder id token request failed"
		}
		return nil, infraerrors.BadRequest("KIRO_BUILDER_ID_TOKEN_FAILED", message)
	}
}

func (s *KiroBuilderIDDeviceFlowService) Credentials(sessionID string) (*KiroBuilderIDCredentials, error) {
	session, err := s.getSession(sessionID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(session.AccessToken) == "" {
		return nil, infraerrors.BadRequest("KIRO_BUILDER_ID_NOT_AUTHORIZED", "builder id device flow is not authorized yet")
	}
	return &KiroBuilderIDCredentials{
		Region:       session.Region,
		ClientID:     session.ClientID,
		ClientSecret: session.ClientSecret,
		AccessToken:  session.AccessToken,
		RefreshToken: session.RefreshToken,
		ExpiresIn:    session.TokenExpiresIn,
		ExpiresAt:    session.TokenExpiresAt,
		ProfileArn:   session.ProfileArn,
		Scopes:       append([]string(nil), session.Scopes...),
	}, nil
}

func (s *KiroBuilderIDDeviceFlowService) postJSON(ctx context.Context, endpoint string, payload any, out any) error {
	body, statusCode, err := s.postJSONRaw(ctx, endpoint, payload)
	if err != nil {
		return err
	}
	if statusCode < 200 || statusCode >= 300 {
		return infraerrors.BadRequest("KIRO_BUILDER_ID_UPSTREAM_FAILED", fmt.Sprintf("builder id request failed: %d", statusCode))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return infraerrors.InternalServer("KIRO_BUILDER_ID_RESPONSE_PARSE_FAILED", "failed to parse builder id response").WithCause(err)
	}
	return nil
}

func (s *KiroBuilderIDDeviceFlowService) postJSONRaw(ctx context.Context, endpoint string, payload any) ([]byte, int, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, infraerrors.InternalServer("KIRO_BUILDER_ID_REQUEST_ENCODE_FAILED", "failed to encode builder id request").WithCause(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, 0, infraerrors.InternalServer("KIRO_BUILDER_ID_REQUEST_BUILD_FAILED", "failed to build builder id request").WithCause(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, 0, infraerrors.BadRequest("KIRO_BUILDER_ID_REQUEST_FAILED", "failed to reach builder id service")
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, 0, infraerrors.InternalServer("KIRO_BUILDER_ID_RESPONSE_READ_FAILED", "failed to read builder id response").WithCause(readErr)
	}
	return body, resp.StatusCode, nil
}

func (s *KiroBuilderIDDeviceFlowService) getSession(sessionID string) (*kiroBuilderIDSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupExpiredLocked(time.Now())
	session, ok := s.sessions[strings.TrimSpace(sessionID)]
	if !ok {
		return nil, infraerrors.BadRequest("KIRO_BUILDER_ID_SESSION_NOT_FOUND", "builder id device flow session not found")
	}
	return cloneKiroBuilderIDSession(session), nil
}

func (s *KiroBuilderIDDeviceFlowService) deleteSession(sessionID string) {
	s.mu.Lock()
	delete(s.sessions, strings.TrimSpace(sessionID))
	s.mu.Unlock()
}

func (s *KiroBuilderIDDeviceFlowService) cleanupExpiredLocked(now time.Time) {
	for id, session := range s.sessions {
		if session.isExpired(now) {
			delete(s.sessions, id)
		}
	}
}

func (s *kiroBuilderIDSession) isExpired(now time.Time) bool {
	return now.After(s.CreatedAt.Add(time.Duration(s.ExpiresIn) * time.Second))
}

func (s *kiroBuilderIDSession) remainingSessionTTL() int64 {
	remaining := s.CreatedAt.Add(time.Duration(s.ExpiresIn)*time.Second).Unix() - time.Now().Unix()
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (s *kiroBuilderIDSession) authorizedAtUnix() int64 {
	if s.AuthorizedAt == nil {
		return 0
	}
	return s.AuthorizedAt.Unix()
}

func cloneKiroBuilderIDSession(session *kiroBuilderIDSession) *kiroBuilderIDSession {
	if session == nil {
		return nil
	}
	clone := *session
	clone.Scopes = append([]string(nil), session.Scopes...)
	return &clone
}

func kiroBuilderIDOIDCURL(region, path string) string {
	return fmt.Sprintf("https://oidc.%s.amazonaws.com%s", normalizeKiroBuilderIDRegion(region), path)
}

func normalizeKiroBuilderIDRegion(region string) string {
	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return "us-east-1"
	}
	if !kiroBuilderIDRegionPattern.MatchString(region) {
		return "us-east-1"
	}
	return region
}

func remainingSeconds(expiresAt int64) int64 {
	if expiresAt <= 0 {
		return 0
	}
	remaining := expiresAt - time.Now().Unix()
	if remaining < 0 {
		return 0
	}
	return remaining
}

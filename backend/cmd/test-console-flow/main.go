package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// 按参考项目流程验证导出的 SSO 凭证：
// 1. SSO Cookie → GET /v1/usage（配额）
// 2. SSO + P-256 DPoP proof → POST /v1/dpop/token（换 DPoP access token）
// 3. DPoP token + per-request DPoP proof → POST /v1/responses（真实推理）

func main() {
	ssoToken := os.Getenv("SSO_TOKEN")
	if ssoToken == "" {
		log.Fatal("SSO_TOKEN required")
	}
	proxyURL := os.Getenv("PROXY_URL")
	if proxyURL == "" {
		proxyURL = "http://127.0.0.1:7887"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	parsed, err := url.Parse(proxyURL)
	if err != nil {
		log.Fatalf("parse proxy: %v", err)
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(parsed), TLSClientConfig: &tls.Config{}},
		Timeout:   60 * time.Second,
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatalf("gen key: %v", err)
	}

	// Step 1: usage via SSO cookie
	log.Println("=== Step 1: GET /v1/usage (SSO cookie) ===")
	usageBody := consoleGet(ctx, client, "https://console.x.ai/v1/usage", func(r *http.Request) {
		r.Header.Set("Cookie", "sso="+ssoToken+"; sso-rw="+ssoToken)
	})
	log.Printf("usage: %s", truncate(string(usageBody), 500))

	// Step 2: DPoP token exchange
	log.Println("\n=== Step 2: POST /v1/dpop/token (SSO + DPoP proof) ===")
	tokenURL := "https://console.x.ai/v1/dpop/token"
	tokenProof, err := signDPoP(key, "POST", tokenURL, nil)
	if err != nil {
		log.Fatalf("dpop proof: %v", err)
	}
	body := fmt.Sprintf(`{"jwk":%s}`, jwkJSON(key))
	req, _ := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(body))
	req.Header.Set("Cookie", "sso="+ssoToken+"; sso-rw="+ssoToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("DPoP", tokenProof)
	browserHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("dpop token request: %v", err)
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	resp.Body.Close()
	log.Printf("status=%d body=%s", resp.StatusCode, truncate(string(respBody), 300))
	if resp.StatusCode != 200 {
		log.Fatal("DPoP token exchange FAILED")
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	json.Unmarshal(respBody, &tok)
	log.Printf("✓ DPoP access_token obtained (expires_in=%ds)", tok.ExpiresIn)

	// Step 3: real inference via /v1/responses
	log.Println("\n=== Step 3: POST /v1/responses (DPoP auth) ===")
	responsesURL := "https://console.x.ai/v1/responses"
	payload := `{"model":"grok-4.5","input":"Reply with exactly one word: pong","max_output_tokens":16}`
	ath := sha256B64(tok.AccessToken)
	rProof, err := signDPoP(key, "POST", responsesURL, &ath)
	if err != nil {
		log.Fatalf("responses dpop proof: %v", err)
	}
	req2, _ := http.NewRequestWithContext(ctx, "POST", responsesURL, strings.NewReader(payload))
	// 参考项目：所有 Console 请求都同时携带 SSO Cookie + DPoP 认证
	req2.Header.Set("Cookie", "sso="+ssoToken+"; sso-rw="+ssoToken)
	req2.Header.Set("Authorization", "DPoP "+tok.AccessToken)
	req2.Header.Set("DPoP", rProof)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("x-cluster", "https://us-east-1.api.x.ai")
	req2.Header.Set("Accept", "*/*")
	req2.Header.Set("Priority", "u=1, i")
	browserHeaders(req2)

	resp2, err := client.Do(req2)
	if err != nil {
		log.Fatalf("responses request: %v", err)
	}
	resp2Body, _ := io.ReadAll(io.LimitReader(resp2.Body, 16384))
	resp2.Body.Close()
	log.Printf("status=%d", resp2.StatusCode)
	log.Printf("body=%s", truncate(string(resp2Body), 1500))

	if resp2.StatusCode == 200 {
		fmt.Println("\n=== RESULT: SSO credential is VALID — full Console chain works (usage ✓ dpop-token ✓ responses ✓) ===")
	} else if resp.StatusCode == 200 {
		fmt.Println("\n=== RESULT: SSO credential is VALID — auth chain works (usage ✓ dpop-token ✓), inference blocked by quota/model ===")
	} else {
		fmt.Println("\n=== RESULT: SSO credential verification incomplete — check logs above ===")
	}
}

func browserHeaders(r *http.Request) {
	r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	r.Header.Set("Origin", "https://console.x.ai")
	r.Header.Set("Referer", "https://console.x.ai/")
	r.Header.Set("Accept", "application/json")
}

func consoleGet(ctx context.Context, c *http.Client, u string, setup func(*http.Request)) []byte {
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	setup(req)
	browserHeaders(req)
	resp, err := c.Do(req)
	if err != nil {
		log.Printf("GET %s error: %v", u, err)
		return nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	log.Printf("GET %s status=%d", u, resp.StatusCode)
	return b
}

func jwkJSON(key *ecdsa.PrivateKey) string {
	x := make([]byte, 32)
	y := make([]byte, 32)
	copy(x[32-len(key.PublicKey.X.Bytes()):], key.PublicKey.X.Bytes())
	copy(y[32-len(key.PublicKey.Y.Bytes()):], key.PublicKey.Y.Bytes())
	jwk := map[string]string{
		"kty": "EC", "crv": "P-256",
		"x": base64.RawURLEncoding.EncodeToString(x),
		"y": base64.RawURLEncoding.EncodeToString(y),
	}
	b, _ := json.Marshal(jwk)
	return string(b)
}

func signDPoP(key *ecdsa.PrivateKey, method, uri string, ath *string) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"jti": randomID(),
		"htm": method,
		"htu": uri,
		"iat": now.Unix(),
	}
	if ath != nil && *ath != "" {
		claims["ath"] = *ath
	}
	x := make([]byte, 32)
	y := make([]byte, 32)
	copy(x[32-len(key.PublicKey.X.Bytes()):], key.PublicKey.X.Bytes())
	copy(y[32-len(key.PublicKey.Y.Bytes()):], key.PublicKey.Y.Bytes())
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["typ"] = "dpop+jwt"
	token.Header["jwk"] = map[string]string{
		"kty": "EC", "crv": "P-256",
		"x": base64.RawURLEncoding.EncodeToString(x),
		"y": base64.RawURLEncoding.EncodeToString(y),
	}
	return token.SignedString(key)
}

func sha256B64(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func randomID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

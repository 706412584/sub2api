package main

import (
	"bufio"
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

// 对比实验：同一问题，带/不带 server tools（web_search），统计 SSE 事件类型。
// 用法：go run ./cmd/test-console-flow [with-tools|no-tools]

func main() {
	ssoToken := os.Getenv("SSO_TOKEN")
	if ssoToken == "" {
		log.Fatal("SSO_TOKEN required")
	}
	variant := "no-tools"
	if len(os.Args) > 1 {
		variant = os.Args[1]
	}
	proxyURL := "http://127.0.0.1:7887"
	ctx := context.Background()

	parsed, _ := url.Parse(proxyURL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(parsed), TLSClientConfig: &tls.Config{}},
		Timeout:   180 * time.Second,
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}

	tokenURL := "https://console.x.ai/v1/dpop/token"
	proof, _ := signDPoP(key, "POST", tokenURL, nil)
	body := fmt.Sprintf(`{"jwk":%s}`, jwkJSON(key))
	req, _ := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(body))
	req.Header.Set("Cookie", "sso="+ssoToken+"; sso-rw="+ssoToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("DPoP", proof)
	browserHeaders(req)
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("dpop token: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Fatalf("dpop token status %d: %s", resp.StatusCode, respBody)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	json.Unmarshal(respBody, &tok)

	responsesURL := "https://console.x.ai/v1/responses"
	payload := map[string]any{
		"model":     "grok-4.5",
		"input":     "鸡兔同笼，35个头94只脚，各几只？请一步步推理。",
		"stream":    true,
		"reasoning": map[string]any{"effort": "low", "summary": "auto"},
	}
	if variant == "with-tools" {
		payload["tools"] = []map[string]any{
			{"type": "web_search"},
			{"type": "x_search"},
		}
	}
	payloadBytes, _ := json.Marshal(payload)

	ath := sha256B64(tok.AccessToken)
	rProof, _ := signDPoP(key, "POST", responsesURL, &ath)
	req2, _ := http.NewRequestWithContext(ctx, "POST", responsesURL, strings.NewReader(string(payloadBytes)))
	req2.Header.Set("Authorization", "DPoP "+tok.AccessToken)
	req2.Header.Set("DPoP", rProof)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("x-cluster", "https://us-east-1.api.x.ai")
	req2.Header.Set("Sec-Fetch-Dest", "empty")
	req2.Header.Set("Sec-Fetch-Mode", "cors")
	req2.Header.Set("Sec-Fetch-Site", "same-origin")
	req2.Header.Set("Priority", "u=1, i")
	browserHeaders(req2)

	resp2, err := client.Do(req2)
	if err != nil {
		log.Fatalf("responses: %v", err)
	}
	defer resp2.Body.Close()
	log.Printf("[%s] status=%d", variant, resp2.StatusCode)
	if resp2.StatusCode != 200 {
		b, _ := io.ReadAll(resp2.Body)
		log.Fatalf("[%s] body: %s", variant, string(b))
	}

	reader := bufio.NewReader(resp2.Body)
	counts := map[string]int{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		jsonStr := strings.TrimPrefix(line, "data: ")
		if jsonStr == "[DONE]" {
			break
		}
		var ev struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(jsonStr), &ev) == nil && ev.Type != "" {
			counts[ev.Type]++
			if ev.Type != "response.output_text.delta" && counts[ev.Type] <= 1 {
				log.Printf("[%s] first [%s]: %s", variant, ev.Type, truncate(jsonStr, 250))
			}
		}
	}
	log.Printf("[%s] === event counts ===", variant)
	for t, c := range counts {
		log.Printf("[%s] %-50s %d", variant, t, c)
	}
}

func browserHeaders(r *http.Request) {
	r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	r.Header.Set("Origin", "https://console.x.ai")
	r.Header.Set("Referer", "https://console.x.ai/")
	r.Header.Set("Accept", "*/*")
}

func jwkJSON(key *ecdsa.PrivateKey) string {
	x := make([]byte, 32)
	y := make([]byte, 32)
	copy(x[32-len(key.PublicKey.X.Bytes()):], key.PublicKey.X.Bytes())
	copy(y[32-len(key.PublicKey.Y.Bytes()):], key.PublicKey.Y.Bytes())
	jwk := map[string]string{"kty": "EC", "crv": "P-256",
		"x": base64.RawURLEncoding.EncodeToString(x),
		"y": base64.RawURLEncoding.EncodeToString(y)}
	b, _ := json.Marshal(jwk)
	return string(b)
}

func signDPoP(key *ecdsa.PrivateKey, method, uri string, ath *string) (string, error) {
	claims := jwt.MapClaims{"jti": uuid(), "htm": method, "htu": uri, "iat": time.Now().Unix()}
	if ath != nil {
		claims["ath"] = *ath
	}
	x := make([]byte, 32)
	y := make([]byte, 32)
	copy(x[32-len(key.PublicKey.X.Bytes()):], key.PublicKey.X.Bytes())
	copy(y[32-len(key.PublicKey.Y.Bytes()):], key.PublicKey.Y.Bytes())
	t := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	t.Header["typ"] = "dpop+jwt"
	t.Header["jwk"] = map[string]string{"kty": "EC", "crv": "P-256",
		"x": base64.RawURLEncoding.EncodeToString(x),
		"y": base64.RawURLEncoding.EncodeToString(y)}
	return t.SignedString(key)
}

func sha256B64(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func uuid() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

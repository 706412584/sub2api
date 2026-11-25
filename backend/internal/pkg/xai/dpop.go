package xai

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// DPoPKeyPair 表示 DPoP 密钥对
type DPoPKeyPair struct {
	privateKey *ecdsa.PrivateKey
	publicKey  *ecdsa.PublicKey
	thumbprint string
}

// GenerateDPoPKeyPair 生成新的 EC P-256 密钥对
func GenerateDPoPKeyPair() (*DPoPKeyPair, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ecdsa key: %w", err)
	}

	thumbprint, err := computeJWKThumbprint(privateKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("compute jwk thumbprint: %w", err)
	}

	return &DPoPKeyPair{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
		thumbprint: thumbprint,
	}, nil
}

// GenerateDPoPProof 生成 DPoP proof JWT
func (kp *DPoPKeyPair) GenerateDPoPProof(method, uri string, accessToken *string) (string, error) {
	now := time.Now().Unix()

	xBytes, yBytes, err := ecPointXYBytes(kp.publicKey)
	if err != nil {
		return "", fmt.Errorf("encode jwk coordinates: %w", err)
	}

	// JWT Header
	header := map[string]any{
		"typ": "dpop+jwt",
		"alg": "ES256",
		"jwk": map[string]any{
			"kty": "EC",
			"crv": "P-256",
			"x":   base64.RawURLEncoding.EncodeToString(xBytes),
			"y":   base64.RawURLEncoding.EncodeToString(yBytes),
		},
	}

	// JWT Claims
	claims := map[string]any{
		"htm": method,
		"htu": uri,
		"iat": now,
		"jti": generateJTI(),
	}

	// 如果有 access token，计算其 SHA-256 thumbprint
	if accessToken != nil && *accessToken != "" {
		hash := sha256.Sum256([]byte(*accessToken))
		claims["ath"] = base64.RawURLEncoding.EncodeToString(hash[:])
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("marshal header: %w", err)
	}

	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}

	// 构造待签名的内容
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerB64 + "." + claimsB64

	// 计算签名
	hash := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, kp.privateKey, hash[:])
	if err != nil {
		return "", fmt.Errorf("ecdsa sign: %w", err)
	}

	// 将 r 和 s 编码为固定 64 字节（P-256 每个 32 字节）
	// 注意：需要左填充零到 32 字节
	signature := make([]byte, 64)
	rBytes := r.Bytes()
	sBytes := s.Bytes()

	// 将 r 右对齐到前 32 字节
	copy(signature[32-len(rBytes):32], rBytes)
	// 将 s 右对齐到后 32 字节
	copy(signature[64-len(sBytes):64], sBytes)

	signatureB64 := base64.RawURLEncoding.EncodeToString(signature)

	return signingInput + "." + signatureB64, nil
}

// GetThumbprint 返回 JWK thumbprint（用于绑定 access token）
func (kp *DPoPKeyPair) GetThumbprint() string {
	return kp.thumbprint
}

// computeJWKThumbprint 计算 RFC 7638 JWK thumbprint
func computeJWKThumbprint(pubKey ecdsa.PublicKey) (string, error) {
	xBytes, yBytes, err := ecPointXYBytes(&pubKey)
	if err != nil {
		return "", fmt.Errorf("encode jwk coordinates: %w", err)
	}

	jwk := map[string]string{
		"crv": "P-256",
		"kty": "EC",
		"x":   base64.RawURLEncoding.EncodeToString(xBytes),
		"y":   base64.RawURLEncoding.EncodeToString(yBytes),
	}

	// 按照 RFC 7638 规范，必须按字典序序列化
	canonical := fmt.Sprintf(`{"crv":"%s","kty":"%s","x":"%s","y":"%s"}`,
		jwk["crv"], jwk["kty"], jwk["x"], jwk["y"])

	hash := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(hash[:]), nil
}

// generateJTI 生成随机 JTI
func generateJTI() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ParseDPoPBoundAccessToken 从 access token 中提取 cnf.jkt（如果存在）
func ParseDPoPBoundAccessToken(accessToken string) (string, bool) {
	claims := DecodeJWTClaims(accessToken)
	if cnf, ok := claims["cnf"].(map[string]any); ok {
		if jkt, ok := cnf["jkt"].(string); ok {
			return jkt, true
		}
	}
	return "", false
}

// ecPointXYBytes 返回 P-256 公钥的固定 32 字节 X/Y 坐标。
// 通过未压缩点编码（PublicKey.Bytes）提取，避免直接读取弃用的 X/Y 字段。
func ecPointXYBytes(pub *ecdsa.PublicKey) ([]byte, []byte, error) {
	raw, err := pub.Bytes()
	if err != nil {
		return nil, nil, err
	}
	if len(raw) != 65 || raw[0] != 0x04 {
		return nil, nil, fmt.Errorf("unexpected uncompressed point encoding: %d bytes", len(raw))
	}
	x := make([]byte, 32)
	y := make([]byte, 32)
	copy(x, raw[1:33])
	copy(y, raw[33:65])
	return x, y, nil
}

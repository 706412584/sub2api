package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"os"
	"strings"

	_ "github.com/lib/pq"
)

// 一次性迁移工具：将遗留的 accounts.extra.sso 加密后迁移到 grok_session_credentials，
// 并从 extra JSONB 中删除该键，避免后续泄露到 DTO 或审计日志。

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/sub2api?sslmode=disable"
	}

	encryptionKey := os.Getenv("TOTP_ENCRYPTION_KEY")
	if encryptionKey == "" {
		log.Fatal("TOTP_ENCRYPTION_KEY environment variable is required")
	}

	keyBytes, err := hex.DecodeString(encryptionKey)
	if err != nil {
		log.Fatalf("Invalid TOTP_ENCRYPTION_KEY (must be 64 hex chars): %v", err)
	}
	if len(keyBytes) != 32 {
		log.Fatalf("TOTP_ENCRYPTION_KEY must be 32 bytes (64 hex chars), got %d bytes", len(keyBytes))
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	log.Println("Starting extra.sso migration...")

	// 查找所有 Grok OAuth 账号且 extra.sso 非空
	rows, err := db.Query(`
		SELECT id, extra
		FROM accounts
		WHERE platform = 'grok'
		  AND type = 'oauth'
		  AND extra IS NOT NULL
		  AND extra::text LIKE '%"sso"%'
		  AND deleted_at IS NULL
	`)
	if err != nil {
		log.Fatalf("Failed to query accounts: %v", err)
	}
	defer rows.Close()

	type accountRow struct {
		ID    int64
		Extra map[string]interface{}
	}

	var accounts []accountRow
	for rows.Next() {
		var id int64
		var extraJSON []byte
		if err := rows.Scan(&id, &extraJSON); err != nil {
			log.Printf("Failed to scan row: %v", err)
			continue
		}

		var extra map[string]interface{}
		if err := json.Unmarshal(extraJSON, &extra); err != nil {
			log.Printf("Failed to unmarshal extra for account %d: %v", id, err)
			continue
		}

		accounts = append(accounts, accountRow{ID: id, Extra: extra})
	}

	log.Printf("Found %d accounts with extra.sso", len(accounts))

	migratedCount := 0
	skippedCount := 0

	for _, acc := range accounts {
		ssoRaw, ok := acc.Extra["sso"]
		if !ok {
			continue
		}

		sso, ok := ssoRaw.(string)
		if !ok || strings.TrimSpace(sso) == "" {
			continue
		}

		// 检查是否已存在 grok_session_credentials
		var exists bool
		err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM grok_session_credentials WHERE account_id = $1)`, acc.ID).Scan(&exists)
		if err != nil {
			log.Printf("Failed to check existing session for account %d: %v", acc.ID, err)
			continue
		}

		if exists {
			log.Printf("Account %d already has grok_session_credentials, skipping", acc.ID)
			skippedCount++
			continue
		}

		// 加密 SSO
		encryptor := newAESEncryptor(keyBytes)
		encryptedSSO, err := encryptor.Encrypt(strings.TrimSpace(sso))
		if err != nil {
			log.Printf("Failed to encrypt SSO for account %d: %v", acc.ID, err)
			continue
		}

		// 事务：插入 grok_session_credentials + 删除 extra.sso
		tx, err := db.Begin()
		if err != nil {
			log.Printf("Failed to begin transaction for account %d: %v", acc.ID, err)
			continue
		}

		_, err = tx.Exec(`
			INSERT INTO grok_session_credentials (
				account_id, source, encrypted_sso, status, created_at, updated_at
			) VALUES ($1, 'build_fallback', $2, 'active', NOW(), NOW())
		`, acc.ID, encryptedSSO)
		if err != nil {
			tx.Rollback()
			log.Printf("Failed to insert session for account %d: %v", acc.ID, err)
			continue
		}

		// 删除 extra.sso
		delete(acc.Extra, "sso")
		newExtraJSON, err := json.Marshal(acc.Extra)
		if err != nil {
			tx.Rollback()
			log.Printf("Failed to marshal new extra for account %d: %v", acc.ID, err)
			continue
		}

		_, err = tx.Exec(`UPDATE accounts SET extra = $1, updated_at = NOW() WHERE id = $2`, newExtraJSON, acc.ID)
		if err != nil {
			tx.Rollback()
			log.Printf("Failed to update extra for account %d: %v", acc.ID, err)
			continue
		}

		if err := tx.Commit(); err != nil {
			log.Printf("Failed to commit transaction for account %d: %v", acc.ID, err)
			continue
		}

		log.Printf("Migrated account %d: extra.sso -> grok_session_credentials", acc.ID)
		migratedCount++
	}

	log.Printf("Migration complete: %d migrated, %d skipped", migratedCount, skippedCount)
}

// 简化的 AES-256-GCM 加密器（复用仓库现有逻辑）

type aesEncryptor struct {
	key []byte
}

func newAESEncryptor(key []byte) *aesEncryptor {
	return &aesEncryptor{key: key}
}

func (e *aesEncryptor) Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

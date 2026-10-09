package repository

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func init() {
	service.RegisterLocalBackupStoreFactory(func(dir string) (service.BackupObjectStore, error) {
		return NewLocalBackupStore(dir)
	})
}

// LocalBackupStore 将备份文件落在服务器本地目录，供未配置 S3 时使用。
type LocalBackupStore struct {
	dir string
}

// NewLocalBackupStore 创建本地备份存储；dir 必须是可写路径。
func NewLocalBackupStore(dir string) (*LocalBackupStore, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("local backup dir is empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create local backup dir: %w", err)
	}
	return &LocalBackupStore{dir: dir}, nil
}

func (s *LocalBackupStore) resolvePath(key string) (string, error) {
	// 统一为正斜杠后再 Clean，避免 Windows 下 "\" 与 "/" 混用导致空 key 误判。
	normalized := strings.ReplaceAll(strings.TrimSpace(key), "\\", "/")
	if normalized == "" {
		return "", fmt.Errorf("invalid backup key")
	}
	clean := filepath.ToSlash(filepath.Clean("/" + normalized))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("invalid backup key")
	}
	full := filepath.Join(s.dir, filepath.FromSlash(clean))
	// 确保最终路径仍在 dir 内，防止路径穿越。
	rel, err := filepath.Rel(s.dir, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid backup key path")
	}
	return full, nil
}

func (s *LocalBackupStore) Upload(_ context.Context, key string, body io.Reader, _ string) (int64, error) {
	full, err := s.resolvePath(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return 0, fmt.Errorf("create backup parent dir: %w", err)
	}
	tmp := full + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return 0, fmt.Errorf("create temp backup file: %w", err)
	}
	n, copyErr := io.Copy(f, body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("write local backup: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("close local backup: %w", closeErr)
	}
	if err := os.Rename(tmp, full); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("finalize local backup: %w", err)
	}
	return n, nil
}

func (s *LocalBackupStore) Download(_ context.Context, key string) (io.ReadCloser, error) {
	full, err := s.resolvePath(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, fmt.Errorf("open local backup: %w", err)
	}
	return f, nil
}

func (s *LocalBackupStore) Delete(_ context.Context, key string) error {
	full, err := s.resolvePath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// PresignURL 本地存储不支持预签名；调用方应改走鉴权下载接口。
func (s *LocalBackupStore) PresignURL(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", fmt.Errorf("local backup store does not support presigned URLs")
}

// HeadBucket 本地目录已在构造时创建，始终可用。
func (s *LocalBackupStore) HeadBucket(_ context.Context) error {
	info, err := os.Stat(s.dir)
	if err != nil {
		return fmt.Errorf("local backup dir unavailable: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("local backup path is not a directory")
	}
	return nil
}

// UploadFile 将本地文件内容上传到存储。
func (s *LocalBackupStore) UploadFile(ctx context.Context, key string, filePath string, _ string) (int64, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("open upload file: %w", err)
	}
	defer func() { _ = f.Close() }()

	return s.Upload(ctx, key, f, "application/gzip")
}

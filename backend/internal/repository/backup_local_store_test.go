//go:build unit

package repository

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalBackupStore_UploadDownloadDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalBackupStore(dir)
	require.NoError(t, err)

	key := "local/2026/07/28/test.sql.gz"
	payload := []byte("gzip-payload")
	n, err := store.Upload(context.Background(), key, bytes.NewReader(payload), "application/gzip")
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), n)

	rc, err := store.Download(context.Background(), key)
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	_ = rc.Close()
	require.Equal(t, payload, got)

	require.NoError(t, store.Delete(context.Background(), key))
	_, err = store.Download(context.Background(), key)
	require.Error(t, err)
}

func TestLocalBackupStore_PathStaysInsideDir(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalBackupStore(dir)
	require.NoError(t, err)

	// "../x" 会被 Clean 成相对路径并落在 dir 内，不得写出 dir 父目录。
	_, err = store.Upload(context.Background(), "../escape.sql.gz", bytes.NewReader([]byte("safe")), "application/gzip")
	require.NoError(t, err)
	parentEscape := filepath.Join(filepath.Dir(dir), "escape.sql.gz")
	_, statErr := os.Stat(parentEscape)
	require.True(t, os.IsNotExist(statErr), "must not write outside backup dir")

	// 空 / 点路径应拒绝
	_, err = store.resolvePath("")
	require.Error(t, err)
	_, err = store.resolvePath(".")
	require.Error(t, err)
	_, err = store.resolvePath("..")
	require.Error(t, err)
}

func TestLocalBackupStore_PresignUnsupported(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalBackupStore(dir)
	require.NoError(t, err)
	_, err = store.PresignURL(context.Background(), "local/a.sql.gz", 0)
	require.Error(t, err)
	require.NoError(t, store.HeadBucket(context.Background()))
}

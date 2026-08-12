package service

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	settingKeyBackupS3Config = "backup_s3_config"
	settingKeyBackupSchedule = "backup_schedule"
	settingKeyBackupRecords  = "backup_records"

	maxBackupRecords = 100
)

var (
	ErrBackupS3NotConfigured = infraerrors.BadRequest("BACKUP_S3_NOT_CONFIGURED", "backup S3 storage is not configured")
	ErrBackupNotFound        = infraerrors.NotFound("BACKUP_NOT_FOUND", "backup record not found")
	ErrBackupInProgress      = infraerrors.Conflict("BACKUP_IN_PROGRESS", "a backup is already in progress")
	ErrRestoreInProgress     = infraerrors.Conflict("RESTORE_IN_PROGRESS", "a restore is already in progress")
	ErrBackupRecordsCorrupt  = infraerrors.InternalServer("BACKUP_RECORDS_CORRUPT", "backup records data is corrupted")
	ErrBackupS3ConfigCorrupt = infraerrors.InternalServer("BACKUP_S3_CONFIG_CORRUPT", "backup S3 config data is corrupted")

	// ErrSecretEncryptionKeyNotConfigured is returned when an S3 SecretAccessKey
	// would be encrypted with an auto-generated (ephemeral) key. That key is
	// regenerated on every process start, so the persisted ciphertext becomes
	// undecryptable after a restart/upgrade ("cipher: message authentication
	// failed"), silently breaking S3 backup/image storage (#4524). Mirrors the
	// existing guards for payments (payment.ProvideEncryptionKey) and TOTP
	// enablement, which likewise refuse to depend on an auto-generated key.
	ErrSecretEncryptionKeyNotConfigured = infraerrors.BadRequest(
		"SECRET_ENCRYPTION_KEY_NOT_CONFIGURED",
		"cannot store the S3 secret access key: no fixed secret encryption key is configured, so the auto-generated key would change on every restart and make the stored secret undecryptable after a restart or upgrade. Set a fixed TOTP_ENCRYPTION_KEY (e.g. generate one with `openssl rand -hex 32`) and try again",
	)
)

// ─── 接口定义 ───

// DBDumper abstracts database dump/restore operations
type DBDumper interface {
	Dump(ctx context.Context) (io.ReadCloser, error)
	Restore(ctx context.Context, data io.Reader) error
}

// BackupObjectStore abstracts object storage for backup files
type BackupObjectStore interface {
	Upload(ctx context.Context, key string, body io.Reader, contentType string) (sizeBytes int64, err error)
	Download(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	PresignURL(ctx context.Context, key string, expiry time.Duration) (string, error)
	HeadBucket(ctx context.Context) error
}

// BackupObjectStoreFactory creates an object store from S3 config
type BackupObjectStoreFactory func(ctx context.Context, cfg *BackupS3Config) (BackupObjectStore, error)

// ─── 数据模型 ───

// BackupS3Config S3 兼容存储配置（支持 Cloudflare R2）
type BackupS3Config struct {
	Endpoint        string `json:"endpoint"` // e.g. https://<account_id>.r2.cloudflarestorage.com
	Region          string `json:"region"`   // R2 用 "auto"
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key,omitempty"` //nolint:revive // field name follows AWS convention
	Prefix          string `json:"prefix"`                      // S3 key 前缀，如 "backups/"
	ForcePathStyle  bool   `json:"force_path_style"`
}

// IsConfigured 检查必要字段是否已配置
func (c *BackupS3Config) IsConfigured() bool {
	return c.Bucket != "" && c.AccessKeyID != "" && c.SecretAccessKey != ""
}

// BackupScheduleConfig 定时备份配置
type BackupScheduleConfig struct {
	Enabled     bool   `json:"enabled"`
	CronExpr    string `json:"cron_expr"`    // cron 表达式，如 "0 2 * * *" 每天凌晨2点
	RetainDays  int    `json:"retain_days"`  // 备份文件过期天数，默认14，0=不自动清理
	RetainCount int    `json:"retain_count"` // 最多保留份数，0=不限制
}

// BackupRecord 备份记录
type BackupRecord struct {
	ID         string `json:"id"`
	Status     string `json:"status"`      // pending, running, completed, failed
	BackupType string `json:"backup_type"` // postgres
	FileName   string `json:"file_name"`
	S3Key      string `json:"s3_key"`
	Parts         []BackupPart `json:"parts,omitempty"`
	// Storage 标识备份落点：s3 或 local。空值兼容旧记录，按 s3 处理。
	Storage       string `json:"storage,omitempty"`
	SizeBytes     int64  `json:"size_bytes"`
	TriggeredBy   string `json:"triggered_by"` // manual, scheduled
	ErrorMsg      string `json:"error_message,omitempty"`
	StartedAt     string `json:"started_at"`
	FinishedAt    string `json:"finished_at,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`     // 过期时间
	Progress      string `json:"progress,omitempty"`       // "dumping", "uploading", ""
	RestoreStatus string `json:"restore_status,omitempty"` // "", "running", "completed", "failed"
	RestoreError  string `json:"restore_error,omitempty"`
	RestoredAt    string `json:"restored_at,omitempty"`
}

// BackupDownloadInfo 描述前端如何取回备份文件。
// mode=presign 时使用 url；mode=proxy 时走鉴权代理下载接口。
type BackupDownloadInfo struct {
	Mode    string `json:"mode"` // presign | proxy
	URL     string `json:"url,omitempty"`
	Storage string `json:"storage"`
	Parts   []BackupDownloadPart `json:"parts,omitempty"`
}

// BackupService 数据库备份恢复服务
type BackupService struct {
	settingRepo SettingRepository
	dbCfg       *config.DatabaseConfig
	encryptor   SecretEncryptor
	// encryptionKeyConfigured mirrors cfg.Totp.EncryptionKeyConfigured: false
	// means the secret encryption key was auto-generated and does not survive a
	// restart. Durable-secret writers must refuse to persist new secrets in that
	// mode (#4524).
	encryptionKeyConfigured bool
	storeFactory            BackupObjectStoreFactory
	localDir                string
	localStoreFactory       func(dir string) (BackupObjectStore, error)
	dumper                  DBDumper

	opMu      sync.Mutex // 保护 backingUp/restoring 标志
	backingUp bool
	restoring bool

	storeMu   sync.Mutex // 保护 store/s3Cfg/local 缓存
	store     BackupObjectStore
	storeKind string // s3 | local
	s3Cfg     *BackupS3Config

	recordsMu sync.Mutex // 保护 records 的 load/save 操作

	cronMu      sync.Mutex
	cronSched   *cron.Cron
	cronEntryID cron.EntryID

	wg           sync.WaitGroup     // 追踪活跃的备份/恢复 goroutine
	shuttingDown atomic.Bool        // 阻止新备份启动
	bgCtx        context.Context    // 所有后台操作的 parent context
	bgCancel     context.CancelFunc // 取消所有活跃后台操作
}

func NewBackupService(
	settingRepo SettingRepository,
	cfg *config.Config,
	encryptor SecretEncryptor,
	storeFactory BackupObjectStoreFactory,
	dumper DBDumper,
) *BackupService {
	bgCtx, bgCancel := context.WithCancel(context.Background())
	dataDir := "./data"
	if cfg != nil && strings.TrimSpace(cfg.Pricing.DataDir) != "" {
		dataDir = strings.TrimSpace(cfg.Pricing.DataDir)
	}
	return &BackupService{
		settingRepo:             settingRepo,
		dbCfg:                   &cfg.Database,
		encryptor:               encryptor,
		encryptionKeyConfigured: cfg.Totp.EncryptionKeyConfigured,
		storeFactory:            storeFactory,
		localDir:                filepath.Join(dataDir, "backups"),
		dumper:                  dumper,
		bgCtx:                   bgCtx,
		bgCancel:                bgCancel,
	}
}

// Start 启动定时备份调度器并清理孤立记录
func (s *BackupService) Start() {
	s.cronSched = cron.New()
	s.cronSched.Start()

	// 清理重启后孤立的 running 记录
	s.recoverStaleRecords()

	// 加载已有的定时配置
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	schedule, err := s.GetSchedule(ctx)
	if err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 加载定时备份配置失败: %v", err)
		return
	}
	if schedule.Enabled && schedule.CronExpr != "" {
		if err := s.applyCronSchedule(schedule); err != nil {
			logger.LegacyPrintf("service.backup", "[Backup] 应用定时备份配置失败: %v", err)
		}
	}
}

// recoverStaleRecords 启动时将孤立的 running 记录标记为 failed
func (s *BackupService) recoverStaleRecords() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	records, err := s.loadRecords(ctx)
	if err != nil {
		return
	}
	for i := range records {
		if records[i].Status == "running" {
			records[i].Status = "failed"
			records[i].ErrorMsg = "interrupted by server restart"
			records[i].Progress = ""
			records[i].FinishedAt = time.Now().Format(time.RFC3339)
			_ = s.saveRecord(ctx, &records[i])
			logger.LegacyPrintf("service.backup", "[Backup] recovered stale running record: %s", records[i].ID)
		}
		if records[i].RestoreStatus == "running" {
			records[i].RestoreStatus = "failed"
			records[i].RestoreError = "interrupted by server restart"
			_ = s.saveRecord(ctx, &records[i])
			logger.LegacyPrintf("service.backup", "[Backup] recovered stale restoring record: %s", records[i].ID)
		}
	}
}

// Stop 停止定时备份并等待活跃操作完成
func (s *BackupService) Stop() {
	s.shuttingDown.Store(true)

	s.cronMu.Lock()
	if s.cronSched != nil {
		s.cronSched.Stop()
	}
	s.cronMu.Unlock()

	// 等待活跃备份/恢复完成（最多 5 分钟）
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		logger.LegacyPrintf("service.backup", "[Backup] all active operations finished")
	case <-time.After(5 * time.Minute):
		logger.LegacyPrintf("service.backup", "[Backup] shutdown timeout after 5min, cancelling active operations")
		if s.bgCancel != nil {
			s.bgCancel() // 取消所有后台操作
		}
		// 给 goroutine 时间响应取消并完成清理
		select {
		case <-done:
			logger.LegacyPrintf("service.backup", "[Backup] active operations cancelled and cleaned up")
		case <-time.After(10 * time.Second):
			logger.LegacyPrintf("service.backup", "[Backup] goroutine cleanup timed out")
		}
	}
}

// ─── S3 配置管理 ───

// EncryptionKeyConfigured reports whether a fixed (explicitly configured) secret
// encryption key is in use. When false the key is auto-generated on every start
// and secrets encrypted with it cannot be recovered after a restart, so callers
// that persist durable secrets must refuse to do so (#4524).
func (s *BackupService) EncryptionKeyConfigured() bool {
	return s != nil && s.encryptionKeyConfigured
}

func (s *BackupService) GetS3Config(ctx context.Context) (*BackupS3Config, error) {
	cfg, err := s.loadS3Config(ctx)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return &BackupS3Config{}, nil
	}
	// 脱敏返回
	cfg.SecretAccessKey = ""
	return cfg, nil
}

func (s *BackupService) UpdateS3Config(ctx context.Context, cfg BackupS3Config) (*BackupS3Config, error) {
	// 如果没提供 secret，保留原有值
	if cfg.SecretAccessKey == "" {
		old, _ := s.loadS3Config(ctx)
		if old != nil {
			cfg.SecretAccessKey = old.SecretAccessKey
		}
	} else {
		// 拒绝用自动生成的临时密钥加密：该密钥每次重启都会变化，落库的密文在
		// 重启/升级后无法解密（#4524）。与支付、TOTP 的处理保持一致。
		if !s.encryptionKeyConfigured {
			return nil, ErrSecretEncryptionKeyNotConfigured
		}
		// 加密 SecretAccessKey
		encrypted, err := s.encryptor.Encrypt(cfg.SecretAccessKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt secret: %w", err)
		}
		cfg.SecretAccessKey = encrypted
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal s3 config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, settingKeyBackupS3Config, string(data)); err != nil {
		return nil, fmt.Errorf("save s3 config: %w", err)
	}

	// 清除缓存的 S3 客户端
	s.storeMu.Lock()
	s.store = nil
	s.s3Cfg = nil
	s.storeMu.Unlock()

	cfg.SecretAccessKey = ""
	return &cfg, nil
}

func (s *BackupService) TestS3Connection(ctx context.Context, cfg BackupS3Config) error {
	// 如果没提供 secret，用已保存的
	if cfg.SecretAccessKey == "" {
		old, _ := s.loadS3Config(ctx)
		if old != nil {
			cfg.SecretAccessKey = old.SecretAccessKey
		}
	}

	if cfg.Bucket == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return fmt.Errorf("incomplete S3 config: bucket, access_key_id, secret_access_key are required")
	}

	store, err := s.storeFactory(ctx, &cfg)
	if err != nil {
		return err
	}
	return store.HeadBucket(ctx)
}

// ─── 定时备份管理 ───

func (s *BackupService) GetSchedule(ctx context.Context) (*BackupScheduleConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupSchedule)
	if err != nil || raw == "" {
		return &BackupScheduleConfig{}, nil
	}
	var cfg BackupScheduleConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return &BackupScheduleConfig{}, nil
	}
	return &cfg, nil
}

func (s *BackupService) UpdateSchedule(ctx context.Context, cfg BackupScheduleConfig) (*BackupScheduleConfig, error) {
	if cfg.Enabled && cfg.CronExpr == "" {
		return nil, infraerrors.BadRequest("INVALID_CRON", "cron expression is required when schedule is enabled")
	}
	// 验证 cron 表达式
	if cfg.CronExpr != "" {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(cfg.CronExpr); err != nil {
			return nil, infraerrors.BadRequest("INVALID_CRON", fmt.Sprintf("invalid cron expression: %v", err))
		}
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal schedule config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, settingKeyBackupSchedule, string(data)); err != nil {
		return nil, fmt.Errorf("save schedule config: %w", err)
	}

	// 应用或停止定时任务
	if cfg.Enabled {
		if err := s.applyCronSchedule(&cfg); err != nil {
			return nil, err
		}
	} else {
		s.removeCronSchedule()
	}

	return &cfg, nil
}

func (s *BackupService) applyCronSchedule(cfg *BackupScheduleConfig) error {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()

	if s.cronSched == nil {
		return fmt.Errorf("cron scheduler not initialized")
	}

	// 移除旧任务
	if s.cronEntryID != 0 {
		s.cronSched.Remove(s.cronEntryID)
		s.cronEntryID = 0
	}

	entryID, err := s.cronSched.AddFunc(cfg.CronExpr, func() {
		s.runScheduledBackup()
	})
	if err != nil {
		return infraerrors.BadRequest("INVALID_CRON", fmt.Sprintf("failed to schedule: %v", err))
	}
	s.cronEntryID = entryID
	logger.LegacyPrintf("service.backup", "[Backup] 定时备份已启用: %s", cfg.CronExpr)
	return nil
}

func (s *BackupService) removeCronSchedule() {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	if s.cronSched != nil && s.cronEntryID != 0 {
		s.cronSched.Remove(s.cronEntryID)
		s.cronEntryID = 0
		logger.LegacyPrintf("service.backup", "[Backup] 定时备份已停用")
	}
}

func (s *BackupService) runScheduledBackup() {
	s.wg.Add(1)
	defer s.wg.Done()

	ctx, cancel := context.WithTimeout(s.bgCtx, 30*time.Minute)
	defer cancel()

	// 读取定时备份配置中的过期天数
	schedule, _ := s.GetSchedule(ctx)
	expireDays := 14 // 默认14天过期
	if schedule != nil && schedule.RetainDays > 0 {
		expireDays = schedule.RetainDays
	}

	logger.LegacyPrintf("service.backup", "[Backup] 开始执行定时备份, 过期天数: %d", expireDays)
	record, err := s.CreateBackup(ctx, "scheduled", expireDays)
	if err != nil {
		if errors.Is(err, ErrBackupInProgress) {
			logger.LegacyPrintf("service.backup", "[Backup] 定时备份跳过: 已有备份正在进行中")
		} else {
			logger.LegacyPrintf("service.backup", "[Backup] 定时备份失败: %v", err)
		}
		return
	}
	logger.LegacyPrintf("service.backup", "[Backup] 定时备份完成: id=%s size=%d", record.ID, record.SizeBytes)

	// 清理过期备份（复用已加载的 schedule）
	if schedule == nil {
		return
	}
	if err := s.cleanupOldBackups(ctx, schedule); err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 清理过期备份失败: %v", err)
	}
}

// ─── 备份/恢复核心 ───

// CreateBackup 创建全量数据库备份并上传到对象存储（S3 优先，未配置时回退本地目录，流式处理）
// expireDays: 备份过期天数，0=永不过期，默认14天
func (s *BackupService) CreateBackup(ctx context.Context, triggeredBy string, expireDays int) (*BackupRecord, error) {
	if s.shuttingDown.Load() {
		return nil, infraerrors.ServiceUnavailable("SERVER_SHUTTING_DOWN", "server is shutting down")
	}

	s.opMu.Lock()
	if s.backingUp {
		s.opMu.Unlock()
		return nil, ErrBackupInProgress
	}
	s.backingUp = true
	s.opMu.Unlock()
	defer func() {
		s.opMu.Lock()
		s.backingUp = false
		s.opMu.Unlock()
	}()

	objectStore, storage, objectKey, err := s.resolveObjectStore(ctx, "")
	if err != nil {
		return nil, err
	}

	now := time.Now()
	backupID := uuid.New().String()[:8]
	fileName := fmt.Sprintf("%s_%s.sql.gz", s.dbCfg.DBName, now.Format("20060102_150405"))
	if objectKey == "" {
		objectKey = s.buildObjectKey(storage, fileName)
	} else {
		// resolveObjectStore 只返回 store，key 在这里拼。
		objectKey = s.buildObjectKey(storage, fileName)
	}

	var expiresAt string
	if expireDays > 0 {
		expiresAt = now.AddDate(0, 0, expireDays).Format(time.RFC3339)
	}

	record := &BackupRecord{
		ID:          backupID,
		Status:      "running",
		BackupType:  "postgres",
		FileName:    fileName,
		S3Key:       objectKey,
		Storage:     storage,
		TriggeredBy: triggeredBy,
		StartedAt:   now.Format(time.RFC3339),
		ExpiresAt:   expiresAt,
	}

	// 流式执行: pg_dump -> gzip -> object store upload
	dumpReader, err := s.dumper.Dump(ctx)
	if err != nil {
		record.Status = "failed"
		record.ErrorMsg = fmt.Sprintf("pg_dump failed: %v", err)
		record.FinishedAt = time.Now().Format(time.RFC3339)
		_ = s.saveRecord(ctx, record)
		return record, fmt.Errorf("pg_dump: %w", err)
	}

	// 使用 io.Pipe 将 gzip 压缩数据流式传递给上传目标
	pr, pw := io.Pipe()
	gzipDone := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				pw.CloseWithError(fmt.Errorf("gzip goroutine panic: %v", r)) //nolint:errcheck
				gzipDone <- fmt.Errorf("gzip goroutine panic: %v", r)
			}
		}()
		gzWriter := gzip.NewWriter(pw)
		var gzErr error
		_, gzErr = io.Copy(gzWriter, dumpReader)
		if closeErr := gzWriter.Close(); closeErr != nil && gzErr == nil {
			gzErr = closeErr
		}
		if closeErr := dumpReader.Close(); closeErr != nil && gzErr == nil {
			gzErr = closeErr
		}
		if gzErr != nil {
			_ = pw.CloseWithError(gzErr)
		} else {
			_ = pw.Close()
		}
		gzipDone <- gzErr
	}()

	contentType := "application/gzip"
	sizeBytes, err := objectStore.Upload(ctx, objectKey, pr, contentType)
	if err != nil {
		_ = pr.CloseWithError(err) // 确保 gzip goroutine 不会悬挂
		gzErr := <-gzipDone        // 安全等待 gzip goroutine 完成
		record.Status = "failed"
		errMsg := fmt.Sprintf("%s upload failed: %v", storage, err)
		if gzErr != nil {
			errMsg = fmt.Sprintf("gzip/dump failed: %v", gzErr)
		}
		record.ErrorMsg = errMsg
		record.FinishedAt = time.Now().Format(time.RFC3339)
		_ = s.saveRecord(ctx, record)
		return record, fmt.Errorf("backup upload: %w", err)
	}
	<-gzipDone // 确保 gzip goroutine 已退出

	record.SizeBytes = sizeBytes
	record.Status = "completed"
	record.FinishedAt = time.Now().Format(time.RFC3339)
	if err := s.saveRecord(ctx, record); err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 保存备份记录失败: %v", err)
	}

	return record, nil
}

// StartBackup 异步创建备份，立即返回 running 状态的记录
func (s *BackupService) StartBackup(ctx context.Context, triggeredBy string, expireDays int) (*BackupRecord, error) {
	if s.shuttingDown.Load() {
		return nil, infraerrors.ServiceUnavailable("SERVER_SHUTTING_DOWN", "server is shutting down")
	}

	s.opMu.Lock()
	if s.backingUp {
		s.opMu.Unlock()
		return nil, ErrBackupInProgress
	}
	s.backingUp = true
	s.opMu.Unlock()

	// 初始化阶段出错时自动重置标志
	launched := false
	defer func() {
		if !launched {
			s.opMu.Lock()
			s.backingUp = false
			s.opMu.Unlock()
		}
	}()

	// 在返回前解析存储目标，避免 goroutine 中配置被修改
	objectStore, storage, _, err := s.resolveObjectStore(ctx, "")
	if err != nil {
		return nil, err
	}

	now := time.Now()
	backupID := uuid.New().String()[:8]
	fileName := fmt.Sprintf("%s_%s.sql.gz", s.dbCfg.DBName, now.Format("20060102_150405"))
	objectKey := s.buildObjectKey(storage, fileName)

	var expiresAt string
	if expireDays > 0 {
		expiresAt = now.AddDate(0, 0, expireDays).Format(time.RFC3339)
	}

	record := &BackupRecord{
		ID:          backupID,
		Status:      "running",
		BackupType:  "postgres",
		FileName:    fileName,
		S3Key:       objectKey,
		Storage:     storage,
		TriggeredBy: triggeredBy,
		StartedAt:   now.Format(time.RFC3339),
		ExpiresAt:   expiresAt,
		Progress:    "pending",
	}

	if err := s.saveRecord(ctx, record); err != nil {
		return nil, fmt.Errorf("save initial record: %w", err)
	}

	launched = true
	// 在启动 goroutine 前完成拷贝，避免数据竞争
	result := *record

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.opMu.Lock()
			s.backingUp = false
			s.opMu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				logger.LegacyPrintf("service.backup", "[Backup] panic recovered: %v", r)
				record.Status = "failed"
				record.ErrorMsg = fmt.Sprintf("internal panic: %v", r)
				record.Progress = ""
				record.FinishedAt = time.Now().Format(time.RFC3339)
				_ = s.saveRecord(context.Background(), record)
			}
		}()
		s.executeBackup(record, objectStore)
	}()

	return &result, nil
}

// executeBackup 后台执行备份（独立于 HTTP context）
func (s *BackupService) executeBackup(record *BackupRecord, objectStore BackupObjectStore) {
	ctx, cancel := context.WithTimeout(s.bgCtx, 30*time.Minute)
	defer cancel()

	// 阶段1: pg_dump
	record.Progress = "dumping"
	_ = s.saveRecord(ctx, record)

	dumpReader, err := s.dumper.Dump(ctx)
	if err != nil {
		record.Status = "failed"
		record.ErrorMsg = fmt.Sprintf("pg_dump failed: %v", err)
		record.Progress = ""
		record.FinishedAt = time.Now().Format(time.RFC3339)
		_ = s.saveRecord(context.Background(), record)
		return
	}

	// 阶段2: gzip + upload
	record.Progress = "uploading"
	_ = s.saveRecord(ctx, record)

	pr, pw := io.Pipe()
	gzipDone := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				pw.CloseWithError(fmt.Errorf("gzip goroutine panic: %v", r)) //nolint:errcheck
				gzipDone <- fmt.Errorf("gzip goroutine panic: %v", r)
			}
		}()
		gzWriter := gzip.NewWriter(pw)
		var gzErr error
		_, gzErr = io.Copy(gzWriter, dumpReader)
		if closeErr := gzWriter.Close(); closeErr != nil && gzErr == nil {
			gzErr = closeErr
		}
		if closeErr := dumpReader.Close(); closeErr != nil && gzErr == nil {
			gzErr = closeErr
		}
		if gzErr != nil {
			_ = pw.CloseWithError(gzErr)
		} else {
			_ = pw.Close()
		}
		gzipDone <- gzErr
	}()

	contentType := "application/gzip"
	sizeBytes, err := objectStore.Upload(ctx, record.S3Key, pr, contentType)
	if err != nil {
		_ = pr.CloseWithError(err) // 确保 gzip goroutine 不会悬挂
		gzErr := <-gzipDone        // 安全等待 gzip goroutine 完成
		record.Status = "failed"
		errMsg := fmt.Sprintf("S3 upload failed: %v", err)
		if gzErr != nil {
			errMsg = fmt.Sprintf("gzip/dump failed: %v", gzErr)
		}
		record.ErrorMsg = errMsg
		record.Progress = ""
		record.FinishedAt = time.Now().Format(time.RFC3339)
		_ = s.saveRecord(context.Background(), record)
		return
	}
	<-gzipDone // 确保 gzip goroutine 已退出

	record.SizeBytes = sizeBytes
	record.Status = "completed"
	record.Progress = ""
	record.FinishedAt = time.Now().Format(time.RFC3339)
	if err := s.saveRecord(context.Background(), record); err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 保存备份记录失败: %v", err)
	}
}

// RestoreBackup 从 S3 下载备份并流式恢复到数据库
func (s *BackupService) RestoreBackup(ctx context.Context, backupID string) error {
	s.opMu.Lock()
	if s.restoring {
		s.opMu.Unlock()
		return ErrRestoreInProgress
	}
	s.restoring = true
	s.opMu.Unlock()
	defer func() {
		s.opMu.Lock()
		s.restoring = false
		s.opMu.Unlock()
	}()

	record, err := s.GetBackupRecord(ctx, backupID)
	if err != nil {
		return err
	}
	if record.Status != "completed" {
		return infraerrors.BadRequest("BACKUP_NOT_COMPLETED", "can only restore from a completed backup")
	}

	objectStore, _, _, err := s.resolveObjectStore(ctx, record.Storage)
	if err != nil {
		return fmt.Errorf("init object store: %w", err)
	}

	// 从对象存储流式下载
	body, err := objectStore.Download(ctx, record.S3Key)
	if err != nil {
		return fmt.Errorf("backup download failed: %w", err)
	}
	defer func() { _ = body.Close() }()

	// 流式解压 gzip -> psql（不将全部数据加载到内存）
	gzReader, err := gzip.NewReader(body)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gzReader.Close() }()

	// 流式恢复
	if err := s.dumper.Restore(ctx, gzReader); err != nil {
		return fmt.Errorf("pg restore: %w", err)
	}

	return nil
}

// StartRestore 异步恢复备份，立即返回
func (s *BackupService) StartRestore(ctx context.Context, backupID string) (*BackupRecord, error) {
	if s.shuttingDown.Load() {
		return nil, infraerrors.ServiceUnavailable("SERVER_SHUTTING_DOWN", "server is shutting down")
	}

	s.opMu.Lock()
	if s.restoring {
		s.opMu.Unlock()
		return nil, ErrRestoreInProgress
	}
	s.restoring = true
	s.opMu.Unlock()

	// 初始化阶段出错时自动重置标志
	launched := false
	defer func() {
		if !launched {
			s.opMu.Lock()
			s.restoring = false
			s.opMu.Unlock()
		}
	}()

	record, err := s.GetBackupRecord(ctx, backupID)
	if err != nil {
		return nil, err
	}
	if record.Status != "completed" {
		return nil, infraerrors.BadRequest("BACKUP_NOT_COMPLETED", "can only restore from a completed backup")
	}

	objectStore, _, _, err := s.resolveObjectStore(ctx, record.Storage)
	if err != nil {
		return nil, fmt.Errorf("init object store: %w", err)
	}

	record.RestoreStatus = "running"
	_ = s.saveRecord(ctx, record)

	launched = true
	result := *record

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.opMu.Lock()
			s.restoring = false
			s.opMu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				logger.LegacyPrintf("service.backup", "[Backup] restore panic recovered: %v", r)
				record.RestoreStatus = "failed"
				record.RestoreError = fmt.Sprintf("internal panic: %v", r)
				_ = s.saveRecord(context.Background(), record)
			}
		}()
		s.executeRestore(record, objectStore)
	}()

	return &result, nil
}

// executeRestore 后台执行恢复
func (s *BackupService) executeRestore(record *BackupRecord, objectStore BackupObjectStore) {
	ctx, cancel := context.WithTimeout(s.bgCtx, 30*time.Minute)
	defer cancel()

	body, err := objectStore.Download(ctx, record.S3Key)
	if err != nil {
		record.RestoreStatus = "failed"
		record.RestoreError = fmt.Sprintf("backup download failed: %v", err)
		_ = s.saveRecord(context.Background(), record)
		return
	}
	defer func() { _ = body.Close() }()

	gzReader, err := gzip.NewReader(body)
	if err != nil {
		record.RestoreStatus = "failed"
		record.RestoreError = fmt.Sprintf("gzip reader: %v", err)
		_ = s.saveRecord(context.Background(), record)
		return
	}
	defer func() { _ = gzReader.Close() }()

	if err := s.dumper.Restore(ctx, gzReader); err != nil {
		record.RestoreStatus = "failed"
		record.RestoreError = fmt.Sprintf("pg restore: %v", err)
		_ = s.saveRecord(context.Background(), record)
		return
	}

	record.RestoreStatus = "completed"
	record.RestoredAt = time.Now().Format(time.RFC3339)
	if err := s.saveRecord(context.Background(), record); err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 保存恢复记录失败: %v", err)
	}
}

// ─── 备份记录管理 ───

func (s *BackupService) ListBackups(ctx context.Context) ([]BackupRecord, error) {
	records, err := s.loadRecords(ctx)
	if err != nil {
		return nil, err
	}
	// 倒序返回（最新在前）
	sort.Slice(records, func(i, j int) bool {
		return records[i].StartedAt > records[j].StartedAt
	})
	return records, nil
}

func (s *BackupService) GetBackupRecord(ctx context.Context, backupID string) (*BackupRecord, error) {
	records, err := s.loadRecords(ctx)
	if err != nil {
		return nil, err
	}
	for i := range records {
		if records[i].ID == backupID {
			return &records[i], nil
		}
	}
	return nil, ErrBackupNotFound
}

func (s *BackupService) DeleteBackup(ctx context.Context, backupID string) error {
	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()

	records, err := s.loadRecordsLocked(ctx)
	if err != nil {
		return err
	}

	var found *BackupRecord
	var remaining []BackupRecord
	for i := range records {
		if records[i].ID == backupID {
			found = &records[i]
		} else {
			remaining = append(remaining, records[i])
		}
	}
	if found == nil {
		return ErrBackupNotFound
	}

	// 删除对象存储中的文件（S3 或本地）
	if found.S3Key != "" && found.Status == "completed" {
		_ = s.deleteBackupObject(ctx, found.Storage, found.S3Key)
	}

	return s.saveRecordsLocked(ctx, remaining)
}

// GetBackupDownloadURL 获取备份文件预签名下载 URL（仅 S3；本地备份请用 DownloadBackup）。
func (s *BackupService) GetBackupDownloadURL(ctx context.Context, backupID string) (string, error) {
	info, err := s.GetBackupDownloadInfo(ctx, backupID)
	if err != nil {
		return "", err
	}
	if info.Mode != "presign" || info.URL == "" {
		return "", infraerrors.BadRequest("BACKUP_LOCAL_USE_PROXY", "local backups must be downloaded via the authenticated download endpoint")
	}
	return info.URL, nil
}

// GetBackupDownloadInfo 返回前端下载方式：S3 用预签名，本地用鉴权代理。
func (s *BackupService) GetBackupDownloadInfo(ctx context.Context, backupID string) (*BackupDownloadInfo, error) {
	record, err := s.GetBackupRecord(ctx, backupID)
	if err != nil {
		return nil, err
	}
	if record.Status != "completed" {
		return nil, infraerrors.BadRequest("BACKUP_NOT_COMPLETED", "backup is not completed")
	}

	storage := normalizeBackupStorage(record.Storage)
	if storage == "local" {
		return &BackupDownloadInfo{Mode: "proxy", Storage: "local"}, nil
	}

	objectStore, resolvedStorage, _, err := s.resolveObjectStore(ctx, storage)
	if err != nil {
		return nil, err
	}
	if resolvedStorage == "local" {
		return &BackupDownloadInfo{Mode: "proxy", Storage: "local"}, nil
	}

	url, err := objectStore.PresignURL(ctx, record.S3Key, 1*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("presign url: %w", err)
	}
	return &BackupDownloadInfo{Mode: "presign", URL: url, Storage: "s3"}, nil
}

// OpenBackupDownload 打开已完成备份的只读流，供鉴权下载接口使用。
func (s *BackupService) OpenBackupDownload(ctx context.Context, backupID string) (io.ReadCloser, *BackupRecord, error) {
	record, err := s.GetBackupRecord(ctx, backupID)
	if err != nil {
		return nil, nil, err
	}
	if record.Status != "completed" {
		return nil, nil, infraerrors.BadRequest("BACKUP_NOT_COMPLETED", "backup is not completed")
	}
	objectStore, _, _, err := s.resolveObjectStore(ctx, record.Storage)
	if err != nil {
		return nil, nil, err
	}
	body, err := objectStore.Download(ctx, record.S3Key)
	if err != nil {
		return nil, nil, fmt.Errorf("backup download failed: %w", err)
	}
	return body, record, nil
}

// ─── 内部方法 ───

func (s *BackupService) loadS3Config(ctx context.Context) (*BackupS3Config, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupS3Config)
	if err != nil || raw == "" {
		return nil, nil //nolint:nilnil // no config is a valid state
	}
	var cfg BackupS3Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, ErrBackupS3ConfigCorrupt
	}
	// 解密 SecretAccessKey
	if cfg.SecretAccessKey != "" {
		decrypted, err := s.encryptor.Decrypt(cfg.SecretAccessKey)
		if err != nil {
			// 兼容未加密的旧数据：如果解密失败，保持原值
			logger.LegacyPrintf("service.backup", "[Backup] S3 SecretAccessKey 解密失败（可能是旧的未加密数据）: %v", err)
		} else {
			cfg.SecretAccessKey = decrypted
		}
	}
	return &cfg, nil
}

// resolveObjectStore 按 preferred 选择存储：s3 优先；未配置 S3 时回退本地目录。
// preferred 为空表示自动选择。
func (s *BackupService) resolveObjectStore(ctx context.Context, preferred string) (BackupObjectStore, string, string, error) {
	preferred = normalizeBackupStorage(preferred)

	s.storeMu.Lock()
	defer s.storeMu.Unlock()

	// 记录指定 local 时强制本地，避免误用缓存的 S3 store。
	if preferred == "local" {
		store, err := s.getOrCreateLocalStoreLocked()
		if err != nil {
			return nil, "", "", err
		}
		return store, "local", "", nil
	}

	if preferred == "s3" || preferred == "" {
		s3Cfg, err := s.loadS3Config(ctx)
		if err != nil {
			return nil, "", "", err
		}
		if s3Cfg != nil && s3Cfg.IsConfigured() {
			if s.store != nil && s.storeKind == "s3" && s.s3Cfg != nil {
				return s.store, "s3", "", nil
			}
			if s.storeFactory == nil {
				return nil, "", "", ErrBackupS3NotConfigured
			}
			store, err := s.storeFactory(ctx, s3Cfg)
			if err != nil {
				return nil, "", "", err
			}
			s.store = store
			s.storeKind = "s3"
			s.s3Cfg = s3Cfg
			return store, "s3", "", nil
		}
		if preferred == "s3" {
			return nil, "", "", ErrBackupS3NotConfigured
		}
	}

	// 未配置 S3，或 preferred 为空时回退本地。
	store, err := s.getOrCreateLocalStoreLocked()
	if err != nil {
		return nil, "", "", err
	}
	return store, "local", "", nil
}

func (s *BackupService) getOrCreateLocalStoreLocked() (BackupObjectStore, error) {
	if s.store != nil && s.storeKind == "local" {
		return s.store, nil
	}
	dir := strings.TrimSpace(s.localDir)
	if dir == "" {
		dir = filepath.Join("data", "backups")
	}
	var (
		store BackupObjectStore
		err   error
	)
	if s.localStoreFactory != nil {
		store, err = s.localStoreFactory(dir)
	} else {
		// 延迟绑定 repository，避免 service 包循环依赖；测试可注入 localStoreFactory。
		store, err = defaultLocalBackupStoreFactory(dir)
	}
	if err != nil {
		return nil, err
	}
	s.store = store
	s.storeKind = "local"
	s.s3Cfg = nil
	return store, nil
}

// defaultLocalBackupStoreFactory 由 repository 在 init 时注入；未注入时返回明确错误。
var defaultLocalBackupStoreFactory = func(dir string) (BackupObjectStore, error) {
	return nil, fmt.Errorf("local backup store factory is not registered")
}

// RegisterLocalBackupStoreFactory 注册本地备份存储工厂（由 repository 包调用）。
func RegisterLocalBackupStoreFactory(factory func(dir string) (BackupObjectStore, error)) {
	if factory != nil {
		defaultLocalBackupStoreFactory = factory
	}
}

func normalizeBackupStorage(storage string) string {
	switch strings.ToLower(strings.TrimSpace(storage)) {
	case "local":
		return "local"
	case "s3":
		return "s3"
	default:
		return ""
	}
}

func (s *BackupService) buildObjectKey(storage, fileName string) string {
	if normalizeBackupStorage(storage) == "local" {
		return fmt.Sprintf("local/%s/%s", time.Now().Format("2006/01/02"), fileName)
	}
	prefix := "backups"
	if s.s3Cfg != nil {
		if p := strings.TrimRight(s.s3Cfg.Prefix, "/"); p != "" {
			prefix = p
		}
	}
	return fmt.Sprintf("%s/%s/%s", prefix, time.Now().Format("2006/01/02"), fileName)
}

// loadRecords 加载备份记录，区分"无数据"和"数据损坏"
func (s *BackupService) loadRecords(ctx context.Context) ([]BackupRecord, error) {
	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()
	return s.loadRecordsLocked(ctx)
}

// loadRecordsLocked 在已持有 recordsMu 锁的情况下加载记录
func (s *BackupService) loadRecordsLocked(ctx context.Context) ([]BackupRecord, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupRecords)
	if err != nil || raw == "" {
		return nil, nil //nolint:nilnil // no records is a valid state
	}
	var records []BackupRecord
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return nil, ErrBackupRecordsCorrupt
	}
	return records, nil
}

// saveRecordsLocked 在已持有 recordsMu 锁的情况下保存记录
func (s *BackupService) saveRecordsLocked(ctx context.Context, records []BackupRecord) error {
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, settingKeyBackupRecords, string(data))
}

// saveRecord 保存单条记录（带互斥锁保护）
func (s *BackupService) saveRecord(ctx context.Context, record *BackupRecord) error {
	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()

	records, _ := s.loadRecordsLocked(ctx)

	// 更新已有记录或追加
	found := false
	for i := range records {
		if records[i].ID == record.ID {
			records[i] = *record
			found = true
			break
		}
	}
	if !found {
		records = append(records, *record)
	}

	// 限制记录数量
	if len(records) > maxBackupRecords {
		records = records[len(records)-maxBackupRecords:]
	}

	return s.saveRecordsLocked(ctx, records)
}

func (s *BackupService) cleanupOldBackups(ctx context.Context, schedule *BackupScheduleConfig) error {
	if schedule == nil {
		return nil
	}

	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()

	records, err := s.loadRecordsLocked(ctx)
	if err != nil {
		return err
	}

	// 按时间倒序
	sort.Slice(records, func(i, j int) bool {
		return records[i].StartedAt > records[j].StartedAt
	})

	var toDelete []BackupRecord
	var toKeep []BackupRecord

	for i, r := range records {
		shouldDelete := false

		// 按保留份数清理
		if schedule.RetainCount > 0 && i >= schedule.RetainCount {
			shouldDelete = true
		}

		// 按保留天数清理
		if schedule.RetainDays > 0 && r.StartedAt != "" {
			startedAt, err := time.Parse(time.RFC3339, r.StartedAt)
			if err == nil && time.Since(startedAt) > time.Duration(schedule.RetainDays)*24*time.Hour {
				shouldDelete = true
			}
		}

		if shouldDelete && r.Status == "completed" {
			toDelete = append(toDelete, r)
		} else {
			toKeep = append(toKeep, r)
		}
	}

	// 删除对象存储中的文件（S3 或本地）
	for _, r := range toDelete {
		if r.S3Key != "" {
			_ = s.deleteBackupObject(ctx, r.Storage, r.S3Key)
		}
	}

	if len(toDelete) > 0 {
		logger.LegacyPrintf("service.backup", "[Backup] 自动清理了 %d 个过期备份", len(toDelete))
		return s.saveRecordsLocked(ctx, toKeep)
	}
	return nil
}

func (s *BackupService) deleteBackupObject(ctx context.Context, storage, key string) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	objectStore, _, _, err := s.resolveObjectStore(ctx, storage)
	if err != nil {
		return err
	}
	return objectStore.Delete(ctx, key)
}


// BackupPart describes one object in a multi-part backup archive.
type BackupPart struct {
	Index     int    `json:"index"`
	S3Key     string `json:"s3_key"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256,omitempty"`
}

// BackupDownloadPart is a presigned part URL for multi-part download.
type BackupDownloadPart struct {
	Index     int    `json:"index"`
	SizeBytes int64  `json:"size_bytes"`
	URL       string `json:"url"`
}

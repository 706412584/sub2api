package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// PgDumper implements service.DBDumper using pg_dump/psql
type PgDumper struct {
	cfg            *config.DatabaseConfig
	db             *sql.DB
	commandContext func(context.Context, string, ...string) *exec.Cmd
}

// NewPgDumper creates a new PgDumper
func NewPgDumper(cfg *config.Config, db *sql.DB) service.DBDumper {
	return &PgDumper{
		cfg:            &cfg.Database,
		db:             db,
		commandContext: exec.CommandContext,
	}
}

// Dump executes pg_dump and returns a streaming reader of the output
func (d *PgDumper) Dump(ctx context.Context) (io.ReadCloser, error) {
	if d.db == nil {
		return nil, errors.New("acquire backup migration lock: nil sql db")
	}
	lockConn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire backup migration lock connection: %w", err)
	}
	if err := pgAdvisoryLock(ctx, lockConn); err != nil {
		// Lock acquisition can fail after PostgreSQL accepted the request. Discard
		// the session because ownership is ambiguous and advisory locks are session-scoped.
		discardSQLConnection(lockConn)
		return nil, fmt.Errorf("acquire backup migration lock: %w", err)
	}
	releaseLock := func() error {
		return releaseBackupMigrationLock(lockConn)
	}

	args := []string{
		"-h", d.cfg.Host,
		"-p", fmt.Sprintf("%d", d.cfg.Port),
		"-U", d.cfg.User,
		"-d", d.cfg.DBName,
		"--no-owner",
		"--no-acl",
		"--clean",
		"--if-exists",
	}

	// 真实执行路径（未注入 mock commandContext）解析便携 pg_dump/psql 路径；
	// 注入方（测试）直接提供命令构造器，跳过解析。
	bin := "pg_dump"
	if d.commandContext == nil {
		bin, err = resolvePostgresCLI("pg_dump")
		if err != nil {
			return nil, errors.Join(err, releaseLock())
		}
	}
	commandContext := d.commandContext
	if commandContext == nil {
		commandContext = exec.CommandContext
	}
	cmd := commandContext(ctx, bin, args...)
	cmd.Env = withPostgresEnv(cmd.Environ(), d.cfg)
	// 确保同目录 DLL（Windows 便携 Postgres）可被加载。
	if dir := filepath.Dir(bin); dir != "" && dir != "." {
		cmd.Dir = dir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create stdout pipe: %w", err), releaseLock())
	}

	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		return nil, errors.Join(fmt.Errorf("start pg_dump (%s): %w", bin, err), releaseLock())
	}

	return &cmdReadCloser{ReadCloser: stdout, cmd: cmd, release: releaseLock}, nil
}

func releaseBackupMigrationLock(conn *sql.Conn) error {
	unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pgAdvisoryUnlock(unlockCtx, conn); err != nil {
		// A failed unlock must not return a possibly lock-owning session to the pool.
		discardSQLConnection(conn)
		return fmt.Errorf("release backup migration lock: %w", err)
	}
	if err := conn.Close(); err != nil {
		return fmt.Errorf("close backup migration lock connection: %w", err)
	}
	return nil
}

func discardSQLConnection(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

// Restore executes psql to restore from a streaming reader
func (d *PgDumper) Restore(ctx context.Context, data io.Reader) error {
	bin, err := resolvePostgresCLI("psql")
	if err != nil {
		return err
	}

	args := []string{
		"-h", d.cfg.Host,
		"-p", fmt.Sprintf("%d", d.cfg.Port),
		"-U", d.cfg.User,
		"-d", d.cfg.DBName,
		"--single-transaction",
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = withPostgresEnv(cmd.Environ(), d.cfg)
	if dir := filepath.Dir(bin); dir != "" && dir != "." {
		cmd.Dir = dir
	}
	cmd.Stdin = data

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", bin, err, string(output))
	}
	return nil
}

func withPostgresEnv(base []string, cfg *config.DatabaseConfig) []string {
	env := append([]string{}, base...)
	if cfg == nil {
		return env
	}
	if cfg.Password != "" {
		env = append(env, "PGPASSWORD="+cfg.Password)
	}
	if cfg.SSLMode != "" {
		env = append(env, "PGSSLMODE="+cfg.SSLMode)
	}
	return env
}

// resolvePostgresCLI 查找 pg_dump/psql：
// 1) 环境变量 PG_DUMP / PSQL 显式路径
// 2) PG_BIN_DIR / DATABASE_PG_BIN_DIR 目录
// 3) 可执行文件旁或工作目录下的 postgres/bin（native 便携部署）
// 4) PATH
func resolvePostgresCLI(name string) (string, error) {
	exeName := name
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(exeName), ".exe") {
		exeName += ".exe"
	}

	// 显式单文件覆盖
	envKey := strings.ToUpper(name) // PG_DUMP / PSQL — PSQL 非常规，用下面分支
	switch strings.ToLower(name) {
	case "pg_dump":
		if v := strings.TrimSpace(os.Getenv("PG_DUMP")); v != "" {
			if st, err := os.Stat(v); err == nil && !st.IsDir() { //nolint:gosec // G703: 管理员显式配置的本地 pg_dump 路径
				return v, nil
			}
		}
	case "psql":
		if v := strings.TrimSpace(os.Getenv("PSQL")); v != "" {
			if st, err := os.Stat(v); err == nil && !st.IsDir() { //nolint:gosec // G703: 管理员显式配置的本地 psql 路径
				return v, nil
			}
		}
	}
	_ = envKey

	var candidates []string

	for _, dirEnv := range []string{"PG_BIN_DIR", "DATABASE_PG_BIN_DIR", "POSTGRES_BIN_DIR"} {
		if dir := strings.TrimSpace(os.Getenv(dirEnv)); dir != "" {
			candidates = append(candidates, filepath.Join(dir, exeName))
		}
	}

	// 相对当前工作目录 / 可执行文件目录的常见 native 布局
	var roots []string
	if wd, err := os.Getwd(); err == nil && wd != "" {
		roots = append(roots, wd)
	}
	if self, err := os.Executable(); err == nil {
		if resolved, err2 := filepath.EvalSymlinks(self); err2 == nil {
			self = resolved
		}
		roots = append(roots, filepath.Dir(self))
	}
	for _, root := range roots {
		candidates = append(candidates,
			filepath.Join(root, "postgres", "bin", exeName),
			filepath.Join(root, "pgsql", "bin", exeName),
			filepath.Join(root, "postgresql", "bin", exeName),
		)
	}

	for _, c := range candidates {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() { //nolint:gosec // G703: 候选路径来自管理员配置的 PG_BIN_DIR 与可执行文件同目录，非用户输入
			return c, nil
		}
	}

	// PATH 回退
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath(exeName); err == nil {
		return p, nil
	}

	return "", fmt.Errorf("%s not found: set PG_BIN_DIR to your PostgreSQL bin directory (e.g. .../postgres/bin), or add it to PATH", name)
}

// cmdReadCloser wraps a command stdout pipe and waits for the process on Close
type cmdReadCloser struct {
	io.ReadCloser
	cmd       *exec.Cmd
	release   func() error
	closeOnce sync.Once
	closeErr  error
}

func (c *cmdReadCloser) Close() error {
	c.closeOnce.Do(func() {
		var closeErrs []error
		if err := c.ReadCloser.Close(); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("close pg_dump stdout: %w", err))
		}
		if err := c.cmd.Wait(); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("pg_dump exited with error: %w", err))
		}
		if c.release != nil {
			if err := c.release(); err != nil {
				closeErrs = append(closeErrs, err)
			}
		}
		c.closeErr = errors.Join(closeErrs...)
	})
	return c.closeErr
}

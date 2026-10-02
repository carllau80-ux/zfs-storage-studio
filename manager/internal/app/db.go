package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func OpenDB(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析 db_url: %w", err)
	}
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	for i := 0; i < 10; i++ {
		if err = pool.Ping(ctx); err == nil {
			return pool, nil
		}
		time.Sleep(time.Second)
	}
	return nil, fmt.Errorf("PostgreSQL 不可达: %w", err)
}

// Migrate 执行版本化迁移(migrations/000N_*.sql,只前进)。
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return runMigrations(ctx, pool)
}

// MigrateTokens 把历史明文令牌转换为哈希存储(幂等;转换后旧会话仍有效)。
func MigrateTokens(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		UPDATE tokens SET token = 'sha256:' || encode(sha256(token::bytea), 'hex')
		WHERE token NOT LIKE 'sha256:%'`)
	return err
}

// SeedAdmin 首次启动时创建 admin 账号:
// 口令优先取显式传入(来自 0600 文件/环境变量);未提供则生成强随机口令,
// 写入 <dataDir>/initial-admin-password(0600)并只记录路径,不打印口令本身。
func SeedAdmin(ctx context.Context, pool *pgxpool.Pool, dataDir string, initPassword string) error {
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if initPassword == "" {
		b := make([]byte, 18)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		initPassword = base64.RawURLEncoding.EncodeToString(b)
		path := filepath.Join(dataDir, "initial-admin-password")
		if err := os.MkdirAll(dataDir, 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(initPassword+"\n"), 0o600); err != nil {
			return err
		}
		slog.Info("已生成初始管理员口令(首次登录后请立即修改)", "file", path)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(initPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `INSERT INTO users(username,password_hash,role) VALUES('admin',$1,'admin')`, string(h))
	return err
}

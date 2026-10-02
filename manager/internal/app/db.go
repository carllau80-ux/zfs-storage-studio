package app

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

//go:embed schema.sql
var schemaSQL string

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

// SeedUsers 首次启动时写入演示账号(admin/operator/viewer)。
func SeedUsers(ctx context.Context, pool *pgxpool.Pool) error {
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	seeds := []struct{ u, p, r string }{
		{"admin", "admin123", "admin"},
		{"operator", "operator123", "operator"},
		{"viewer", "viewer123", "viewer"},
	}
	for _, s := range seeds {
		h, err := bcrypt.GenerateFromPassword([]byte(s.p), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO users(username,password_hash,role) VALUES($1,$2,$3)`,
			s.u, string(h), s.r); err != nil {
			return err
		}
	}
	return nil
}

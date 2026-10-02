package app

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type migration struct {
	version int
	name    string
	body    string
}

// loadMigrations 读取并按版本排序(migrations/000N_xxx.sql)。
func loadMigrations() ([]migration, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		verStr, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("迁移文件名不规范(应为 000N_name.sql): %s", e.Name())
		}
		ver, err := strconv.Atoi(verStr)
		if err != nil {
			return nil, fmt.Errorf("迁移版本号非法: %s", e.Name())
		}
		b, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: ver, name: e.Name(), body: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// applySQL 逐条执行(去整行注释、按分号切分;幂等 DDL 由迁移文件自身保证)。
func applySQL(ctx context.Context, pool *pgxpool.Pool, content string) error {
	clean := make([]string, 0, 64)
	for _, l := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "--") {
			continue
		}
		clean = append(clean, l)
	}
	for _, stmt := range strings.Split(strings.Join(clean, "\n"), ";") {
		body := strings.TrimSpace(stmt)
		if body == "" {
			continue
		}
		if _, err := pool.Exec(ctx, body); err != nil {
			return fmt.Errorf("执行失败: %w\n语句: %.200s", err, body)
		}
	}
	return nil
}

// runMigrations 只前进:记录已应用版本,新库/存量库均可安全执行(文件为幂等 DDL)。
func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
		  version INT PRIMARY KEY, name TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	applied := map[int]bool{}
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	migs, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, m := range migs {
		if applied[m.version] {
			continue
		}
		start := time.Now()
		if err := applySQL(ctx, pool, m.body); err != nil {
			return fmt.Errorf("迁移 %s 失败: %w", m.name, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO schema_migrations(version,name) VALUES($1,$2)`, m.version, m.name); err != nil {
			return err
		}
		// 存量库兜底:老库结构与 0001 相同,首次执行即视为基线落库
		slogInfoMigration(m.name, time.Since(start))
	}
	return nil
}

func slogInfoMigration(name string, d time.Duration) {
	fmt.Printf("migration applied: %s (%dms)\n", name, d.Milliseconds())
}

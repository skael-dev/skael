package platform

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrate/*.sql
var migrations embed.FS

// PoolConfig holds optional pgxpool tuning parameters. A nil value keeps
// pgxpool's built-in defaults.
type PoolConfig struct {
	MaxConns          int
	MinConns          int
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
}

func NewPool(ctx context.Context, databaseURL string, cfg *PoolConfig) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	if cfg != nil {
		poolCfg.MaxConns = int32(cfg.MaxConns)
		poolCfg.MinConns = int32(cfg.MinConns)
		poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
		poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
		poolCfg.HealthCheckPeriod = cfg.HealthCheckPeriod
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pgxpool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	return Migrate(db)
}

func Migrate(db *sql.DB) error {
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	if err := goose.Up(db, "migrate"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// MigrateUpTo applies migrations up to and including version, leaving later
// ones unapplied. It exists so a migration can be tested against a database
// populated at the prior version — the only way that test means anything. A
// test that opens a fully-migrated database and then "upgrades" it passes
// with the migration deleted.
func MigrateUpTo(db *sql.DB, version int64) error {
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	if err := goose.UpTo(db, "migrate", version); err != nil {
		return fmt.Errorf("run migrations up to %d: %w", version, err)
	}
	return nil
}

func MigrateDown(db *sql.DB) error {
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	if err := goose.Down(db, "migrate"); err != nil {
		return fmt.Errorf("rollback migration: %w", err)
	}
	return nil
}

func MigrateStatus(db *sql.DB) error {
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	return goose.Status(db, "migrate")
}

func OpenDB(databaseURL string) (*sql.DB, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

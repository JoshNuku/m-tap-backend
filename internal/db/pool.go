package db

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/boronlabs/m-tap-backend/internal/config"
)

//go:embed migrations/*.sql
var MigrationsFS embed.FS

// InitPool initializes a PostgreSQL connection pool configured for Neon Serverless (PgBouncer mode).
func InitPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	// PgBouncer / Neon Serverless connection bounds
	poolConfig.MaxConns = cfg.DBMaxConns
	poolConfig.MinConns = cfg.DBMinConns
	poolConfig.MaxConnLifetime = cfg.DBMaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.DBMaxConnIdleTime
	poolConfig.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create pgx connection pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping PostgreSQL database: %w", err)
	}

	slog.Info("PostgreSQL connection pool established",
		"max_conns", poolConfig.MaxConns,
		"min_conns", poolConfig.MinConns,
	)

	return pool, nil
}

// RunMigrations executes the embedded idempotent SQL schema migrations.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	migrationSQL, err := MigrationsFS.ReadFile("migrations/000001_init_schema.up.sql")
	if err != nil {
		return fmt.Errorf("failed to read embedded migration file: %w", err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection for migration: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, string(migrationSQL)); err != nil {
		return fmt.Errorf("failed to execute schema migration: %w", err)
	}

	slog.Info("Schema migrations executed successfully")
	return nil
}


package app

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"

	"github.com/insider/insider/internal/config"
	"github.com/insider/insider/internal/migration"
	"github.com/insider/insider/internal/observability"
)

type Infra struct {
	Config *config.Config
	DB     *pgxpool.Pool
	Redis  *redis.Client
	Logger *slog.Logger
}

func NewInfra(ctx context.Context, logger *slog.Logger) (*Infra, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	pgxCfg, err := pgxpool.ParseConfig(cfg.Database.DSN())
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	pgxCfg.MaxConns = int32(cfg.Database.MaxConns)
	pgxCfg.MinConns = int32(cfg.Database.MinConns)
	pgxCfg.MaxConnLifetime = cfg.Database.MaxConnLifetime
	pgxCfg.MaxConnIdleTime = cfg.Database.MaxConnIdleTime
	pgxCfg.HealthCheckPeriod = cfg.Database.HealthCheckPeriod

	pool, err := pgxpool.NewWithConfig(ctx, pgxCfg)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	logger.Info("connected to PostgreSQL",
		"max_conns", cfg.Database.MaxConns,
		"min_conns", cfg.Database.MinConns,
	)

	// Run migrations
	sqlDB, err := sql.Open("pgx", cfg.Database.DSN())
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("open sql db for migrations: %w", err)
	}
	if err := migration.Run(sqlDB); err != nil {
		_ = sqlDB.Close()
		pool.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	_ = sqlDB.Close()
	logger.Info("migrations applied")

	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.Redis.Addr,
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		PoolSize:     cfg.Redis.PoolSize,
		MinIdleConns: cfg.Redis.MinIdleConns,
		PoolTimeout:  cfg.Redis.PoolTimeout,
		ReadTimeout:  cfg.Redis.ReadTimeout,
		WriteTimeout: cfg.Redis.WriteTimeout,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to redis: %w", err)
	}
	logger.Info("connected to Redis",
		"pool_size", cfg.Redis.PoolSize,
		"min_idle_conns", cfg.Redis.MinIdleConns,
	)

	if cfg.Tracing.Enabled {
		shutdown, err := observability.InitTracing(ctx, cfg.Tracing.Endpoint)
		if err != nil {
			logger.Warn("tracing init failed, continuing without tracing", "error", err)
		} else {
			// Store shutdown for deferred call by the caller
			_ = shutdown
			logger.Info("tracing enabled")
		}
	}

	return &Infra{
		Config: cfg,
		DB:     pool,
		Redis:  rdb,
		Logger: logger,
	}, nil
}

func (i *Infra) Close() {
	i.DB.Close()
	_ = i.Redis.Close()
}

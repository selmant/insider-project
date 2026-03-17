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

	pool, err := pgxpool.New(ctx, cfg.Database.DSN())
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	logger.Info("connected to PostgreSQL")

	// Run migrations
	sqlDB, err := sql.Open("pgx", cfg.Database.DSN())
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("open sql db for migrations: %w", err)
	}
	if err := migration.Run(sqlDB); err != nil {
		sqlDB.Close()
		pool.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	sqlDB.Close()
	logger.Info("migrations applied")

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to redis: %w", err)
	}
	logger.Info("connected to Redis")

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
	i.Redis.Close()
}

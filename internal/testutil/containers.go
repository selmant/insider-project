package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/insider/insider/internal/migration"
)

type PostgresContainer struct {
	Container *tcpostgres.PostgresContainer
	Pool      *pgxpool.Pool
	DSN       string
}

func SetupPostgres(ctx context.Context) (*PostgresContainer, error) {
	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("insider_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("start postgres container: %w", err)
	}

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("get connection string: %w", err)
	}

	// Run migrations using database/sql (goose requirement)
	sqlDB, err := sql.Open("pgx", connStr)
	if err != nil {
		return nil, fmt.Errorf("open sql connection: %w", err)
	}
	if err := migration.Run(sqlDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	sqlDB.Close()

	// Create pgxpool for the repo
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	return &PostgresContainer{
		Container: container,
		Pool:      pool,
		DSN:       connStr,
	}, nil
}

func (pc *PostgresContainer) Cleanup(ctx context.Context) {
	if pc.Pool != nil {
		pc.Pool.Close()
	}
	if pc.Container != nil {
		_ = pc.Container.Terminate(ctx)
	}
}

// TruncateTables clears all data for test isolation.
func (pc *PostgresContainer) TruncateTables(ctx context.Context) error {
	_, err := pc.Pool.Exec(ctx, "TRUNCATE dead_letters, notifications, templates CASCADE")
	return err
}

type RedisContainer struct {
	Container *tcredis.RedisContainer
	Client    *redis.Client
	Addr      string
}

func SetupRedis(ctx context.Context) (*RedisContainer, error) {
	container, err := tcredis.Run(ctx,
		"redis:7-alpine",
		testcontainers.WithWaitStrategy(
			wait.ForLog("Ready to accept connections").
				WithStartupTimeout(15*time.Second),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("start redis container: %w", err)
	}

	connStr, err := container.ConnectionString(ctx)
	if err != nil {
		return nil, fmt.Errorf("get redis connection string: %w", err)
	}

	opts, err := redis.ParseURL(connStr)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}

	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &RedisContainer{
		Container: container,
		Client:    client,
		Addr:      opts.Addr,
	}, nil
}

func (rc *RedisContainer) Cleanup(ctx context.Context) {
	if rc.Client != nil {
		rc.Client.Close()
	}
	if rc.Container != nil {
		_ = rc.Container.Terminate(ctx)
	}
}

// FlushAll clears all Redis data for test isolation.
func (rc *RedisContainer) FlushAll(ctx context.Context) error {
	return rc.Client.FlushAll(ctx).Err()
}

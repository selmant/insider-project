package config

import (
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	Redis    RedisConfig
	Worker   WorkerConfig
	Provider ProviderConfig
	Tracing  TracingConfig
}

type ServerConfig struct {
	Port            int           `env:"SERVER_PORT" envDefault:"8080"`
	ReadTimeout     time.Duration `env:"SERVER_READ_TIMEOUT" envDefault:"10s"`
	WriteTimeout    time.Duration `env:"SERVER_WRITE_TIMEOUT" envDefault:"30s"`
	ShutdownTimeout time.Duration `env:"SERVER_SHUTDOWN_TIMEOUT" envDefault:"30s"`
}

type DatabaseConfig struct {
	Host     string `env:"DB_HOST" envDefault:"localhost"`
	Port     int    `env:"DB_PORT" envDefault:"5432"`
	User     string `env:"DB_USER" envDefault:"insider"`
	Password string `env:"DB_PASSWORD" envDefault:"insider"`
	Name     string `env:"DB_NAME" envDefault:"insider"`
	SSLMode  string `env:"DB_SSLMODE" envDefault:"disable"`
	MaxConns int    `env:"DB_MAX_CONNS" envDefault:"25"`
}

func (d DatabaseConfig) DSN() string {
	return "postgres://" + d.User + ":" + d.Password + "@" + d.Host + ":" + itoa(d.Port) + "/" + d.Name + "?sslmode=" + d.SSLMode
}

type RedisConfig struct {
	Addr     string `env:"REDIS_ADDR" envDefault:"localhost:6379"`
	Password string `env:"REDIS_PASSWORD" envDefault:""`
	DB       int    `env:"REDIS_DB" envDefault:"0"`
}

type WorkerConfig struct {
	PoolSize      int           `env:"WORKER_POOL_SIZE" envDefault:"10"`
	RateLimit     int           `env:"WORKER_RATE_LIMIT" envDefault:"100"`
	PollInterval  time.Duration `env:"WORKER_POLL_INTERVAL" envDefault:"100ms"`
	RetryBaseWait time.Duration `env:"WORKER_RETRY_BASE_WAIT" envDefault:"1s"`
	RetryMaxWait  time.Duration `env:"WORKER_RETRY_MAX_WAIT" envDefault:"5m"`
	MaxAttempts   int           `env:"WORKER_MAX_ATTEMPTS" envDefault:"5"`
}

type ProviderConfig struct {
	WebhookURL     string        `env:"PROVIDER_WEBHOOK_URL" envDefault:"https://webhook.site"`
	RequestTimeout time.Duration `env:"PROVIDER_REQUEST_TIMEOUT" envDefault:"10s"`
	CBMaxFailures  int           `env:"CB_MAX_FAILURES" envDefault:"5"`
	CBTimeout      time.Duration `env:"CB_TIMEOUT" envDefault:"30s"`
}

type TracingConfig struct {
	Endpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT" envDefault:"localhost:4317"`
	Enabled  bool   `env:"TRACING_ENABLED" envDefault:"false"`
}

func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

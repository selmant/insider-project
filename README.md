# Insider Notification System

Event-driven notification system built with Go, designed to send millions of notifications daily across SMS, Email, and Push channels. Handles burst traffic, retries failed deliveries, and provides real-time status tracking.

## Architecture

```
                         ┌──────────────┐
                         │   REST API   │
                         │  (cmd/api)   │
                         └──────┬───────┘
                                │
               ┌────────────────┼────────────────┐
               │                │                │
        ┌──────▼──────┐  ┌─────▼───────┐  ┌─────▼───────┐
        │  Redis Queue │  │  PostgreSQL  │  │  WebSocket  │
        │ (sorted sets)│  │   (source    │  │    Hub      │
        │              │  │   of truth)  │  │             │
        └──┬───┬───┬───┘  └─────────────┘  └─────────────┘
           │   │   │
     ┌─────▼┐ ┌▼────┐ ┌▼─────┐
     │Worker│ │Worker│ │Worker│
     │ SMS  │ │Email │ │ Push │
     └──┬───┘ └──┬───┘ └──┬───┘
        │        │        │
        └────────┼────────┘
                 │
        ┌────────▼────────┐
        │    Provider      │
        │ (Circuit Breaker │
        │  + Webhook)      │
        └─────────────────┘
```

**Key design decisions:**
- **Separate binaries per channel** — Each worker (`worker-sms`, `worker-email`, `worker-push`) runs as its own process for independent scaling and high availability
- **Redis sorted sets for queuing** — Score = `priority * 1e12 + unix_nano` ensures priority ordering with FIFO within same priority
- **ZPOPMIN for atomic dequeue** — No duplicate processing without distributed locks
- **Sliding window rate limiter** — Lua script in Redis for atomic per-channel rate limiting
- **Circuit breaker** — Protects provider calls with closed/open/half-open state machine

## Tech Stack

- **Go 1.22+** with `net/http` routing (path values)
- **PostgreSQL** (pgx/v5) — source of truth for notifications, templates, dead letters
- **Redis** (go-redis/v9) — priority queues, rate limiting
- **WebSocket** (coder/websocket) — real-time status broadcasting
- **OpenTelemetry** — distributed tracing with Jaeger
- **Goose** — database migrations
- **Docker Compose** — local development environment

## Project Structure

```
cmd/
  api/              API server
  worker-sms/       SMS channel worker
  worker-email/     Email channel worker
  worker-push/      Push channel worker
internal/
  api/              HTTP handlers, router, middleware
  app/              Shared infrastructure and worker wiring
  config/           Environment-based configuration
  domain/           Core entities (Notification, Template, Channel, Status)
  migration/        Embedded SQL migrations (goose)
  observability/    Metrics, tracing, logging
  provider/         Provider interface, circuit breaker, webhook client
  queue/            Queue interfaces and Redis implementation
  repository/       Repository interfaces and PostgreSQL implementation
  retry/            Exponential backoff strategy, dead letter handler
  template/         Template engine with caching
  testutil/         Testcontainers helpers for integration tests
  websocket/        WebSocket hub and client
  worker/           Goroutine pool, dispatcher, processor
api/
  openapi.yaml      OpenAPI 3.0 specification
deployments/
  Dockerfile        Multi-stage build for all binaries
  docker-compose.yaml
  .env.example
```

## Getting Started

### Prerequisites

- Go 1.22+
- Docker and Docker Compose
- PostgreSQL 16+ and Redis 7+ (or use Docker Compose)

### Run with Docker Compose

```bash
# Start all services (API + 3 workers + Postgres + Redis + Jaeger)
make docker-up

# View logs
docker compose -f deployments/docker-compose.yaml logs -f

# Stop
make docker-down
```

The API is available at `http://localhost:8080`. Jaeger UI at `http://localhost:16686`.

### Run Locally

```bash
# Start dependencies
docker compose -f deployments/docker-compose.yaml up -d postgres redis jaeger

# Export environment variables (adjust for local)
export DB_HOST=localhost DB_PORT=5432 DB_USER=insider DB_PASSWORD=insider DB_NAME=insider DB_SSLMODE=disable
export REDIS_ADDR=localhost:6379
export PROVIDER_WEBHOOK_URL=https://webhook.site/your-id

# Run API
make run-api

# Run workers (each in a separate terminal)
make run-worker-sms
make run-worker-email
make run-worker-push
```

### Build

```bash
# Build all binaries to bin/
make build

# Build individually
make build-api
make build-workers
```

## API

### Notifications

```bash
# Create a notification
curl -X POST http://localhost:8080/api/v1/notifications \
  -H "Content-Type: application/json" \
  -d '{
    "recipient": "+1234567890",
    "channel": "sms",
    "content": "Hello World",
    "priority": "high"
  }'

# Create with idempotency key
curl -X POST http://localhost:8080/api/v1/notifications \
  -H "Content-Type: application/json" \
  -d '{
    "recipient": "user@example.com",
    "channel": "email",
    "content": "Welcome!",
    "idempotency_key": "welcome-user-123"
  }'

# Schedule for later
curl -X POST http://localhost:8080/api/v1/notifications \
  -H "Content-Type: application/json" \
  -d '{
    "recipient": "+1234567890",
    "channel": "push",
    "content": "Reminder!",
    "scheduled_at": "2025-01-01T10:00:00Z"
  }'

# Batch create (up to 1000)
curl -X POST http://localhost:8080/api/v1/notifications/batch \
  -H "Content-Type: application/json" \
  -d '{
    "notifications": [
      {"recipient": "+1111111111", "channel": "sms", "content": "Hello 1"},
      {"recipient": "+2222222222", "channel": "sms", "content": "Hello 2"}
    ]
  }'

# Get notification by ID
curl http://localhost:8080/api/v1/notifications/{id}

# Get batch status
curl http://localhost:8080/api/v1/notifications/batch/{batch_id}

# Cancel a notification
curl -X POST http://localhost:8080/api/v1/notifications/{id}/cancel

# List with filters
curl "http://localhost:8080/api/v1/notifications?channel=sms&status=sent&limit=20&offset=0"
```

### Templates

```bash
# Create a template
curl -X POST http://localhost:8080/api/v1/templates \
  -H "Content-Type: application/json" \
  -d '{
    "name": "otp-sms",
    "channel": "sms",
    "content": "Your verification code is {{.Code}}. Expires in {{.Minutes}} minutes.",
    "variables": ["Code", "Minutes"]
  }'

# Use template in notification
curl -X POST http://localhost:8080/api/v1/notifications \
  -H "Content-Type: application/json" \
  -d '{
    "recipient": "+1234567890",
    "channel": "sms",
    "template_id": "{template-uuid}",
    "template_vars": {"Code": "1234", "Minutes": "5"}
  }'

# CRUD
curl http://localhost:8080/api/v1/templates/{id}
curl -X PUT http://localhost:8080/api/v1/templates/{id} -H "Content-Type: application/json" -d '...'
curl -X DELETE http://localhost:8080/api/v1/templates/{id}
curl http://localhost:8080/api/v1/templates
```

### Health & Metrics

```bash
curl http://localhost:8080/health      # {"status":"ok"}
curl http://localhost:8080/ready       # checks Postgres + Redis
curl http://localhost:8080/metrics     # per-channel sent/failed/processing counts
```

### WebSocket

Connect to `ws://localhost:8080/ws/notifications` to receive real-time status events:

```json
{"type":"notification.sent","notification_id":"...","channel":"sms","status":"sent"}
{"type":"notification.failed","notification_id":"...","channel":"email","status":"failed","error":"..."}
{"type":"notification.retrying","notification_id":"...","channel":"push","status":"queued","error":"retry 2/5 in 4s: ..."}
```

## Processing Pipeline

1. **API** receives notification, persists to PostgreSQL, enqueues to Redis
2. **Scheduler** polls DB every second for due scheduled notifications, enqueues them
3. **Dispatcher** polls Redis per-channel with rate limiting, submits to goroutine pool
4. **Processor** fetches notification from DB, sends via provider:
   - **Success** — marks as `sent`, broadcasts WebSocket event
   - **Failure** — increments attempts, re-queues with exponential backoff + jitter
   - **Max retries exhausted** — marks as `failed`, inserts dead letter record
5. **Recovery sweep** — on worker startup, re-enqueues stuck `pending`/`queued` notifications

### Notification Status Flow

```
pending → queued → processing → sent
    │         │         │
    │         │         └→ failed (after max retries → dead_letters)
    │         │
    └→ scheduled → queued (when scheduled_at <= now)
    │
    └→ cancelled (from pending/scheduled/queued)
```

## Configuration

All configuration via environment variables. See `deployments/.env.example` for the full list.

| Variable | Default | Description |
|---|---|---|
| `SERVER_PORT` | 8080 | HTTP server port |
| `DB_HOST` | localhost | PostgreSQL host |
| `DB_MAX_CONNS` | 25 | Max database connections |
| `REDIS_ADDR` | localhost:6379 | Redis address |
| `WORKER_POOL_SIZE` | 10 | Goroutines per channel worker |
| `WORKER_RATE_LIMIT` | 100 | Max sends per second per channel |
| `WORKER_POLL_INTERVAL` | 100ms | Queue poll interval |
| `WORKER_RETRY_BASE_WAIT` | 1s | Initial retry backoff |
| `WORKER_RETRY_MAX_WAIT` | 5m | Maximum retry backoff |
| `WORKER_MAX_ATTEMPTS` | 5 | Max delivery attempts |
| `PROVIDER_WEBHOOK_URL` | — | Webhook endpoint for notifications |
| `CB_MAX_FAILURES` | 5 | Circuit breaker failure threshold |
| `CB_TIMEOUT` | 30s | Circuit breaker recovery timeout |
| `TRACING_ENABLED` | false | Enable OpenTelemetry tracing |

## Testing

```bash
# Unit tests
make test

# Integration tests (requires Docker for testcontainers)
make test-integration

# Lint
make lint
```

Integration tests use [testcontainers-go](https://golang.testcontainers.org/) to spin up real PostgreSQL and Redis instances. No mocks for infrastructure — tests verify actual database queries, Redis operations, and the full processing pipeline.

**Test coverage:**

| Area | Tests | What's verified |
|---|---|---|
| PostgreSQL repos | 16 | CRUD, batch operations, filtering, pagination, scheduled/recovery queries |
| Redis queue | 7 | Enqueue/dequeue, priority ordering, channel isolation, rate limiter |
| API handlers | 16 | Full HTTP stack: notifications, templates, batches, health, metrics |
| Webhook + CB | 4 | HTTP client, server errors, circuit breaker state transitions |
| WebSocket | 3 | Broadcast delivery, multiple clients, disconnect |
| Worker pipeline | 11 | Processor success/failure/retry/dead letter, scheduler, recovery sweep |

## Database Schema

Three tables managed by goose migrations:

- **`notifications`** — Core notification data with indexes on status, batch_id, channel+status, scheduled_at, created_at
- **`templates`** — Reusable notification templates with Go text/template syntax
- **`dead_letters`** — Failed notifications after max retry attempts for investigation

## License

Internal project — Insider One.

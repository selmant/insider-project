//go:build integration

package worker_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/observability"
	"github.com/insider/insider/internal/queue"
	qredis "github.com/insider/insider/internal/queue/redis"
	"github.com/insider/insider/internal/repository/postgres"
	"github.com/insider/insider/internal/retry"
	"github.com/insider/insider/internal/testutil"
	"github.com/insider/insider/internal/websocket"
	"github.com/insider/insider/internal/worker"
)

var (
	pgContainer    *testutil.PostgresContainer
	redisContainer *testutil.RedisContainer
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error
	pgContainer, err = testutil.SetupPostgres(ctx)
	if err != nil {
		panic("failed to setup postgres: " + err.Error())
	}
	defer pgContainer.Cleanup(ctx)

	redisContainer, err = testutil.SetupRedis(ctx)
	if err != nil {
		panic("failed to setup redis: " + err.Error())
	}
	defer redisContainer.Cleanup(ctx)

	m.Run()
}

// --- test provider ---

type testProvider struct {
	mu    sync.Mutex
	calls int
	err   error
	msgID string
}

func (p *testProvider) Send(ctx context.Context, n *domain.Notification) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.msgID, p.err
}

func (p *testProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// --- helpers ---

func newTestNotification(channel domain.Channel, status domain.Status) *domain.Notification {
	now := time.Now().Truncate(time.Microsecond)
	return &domain.Notification{
		ID:          uuid.New(),
		Recipient:   "+1234567890",
		Channel:     channel,
		Content:     "Integration test message",
		Priority:    domain.PriorityNormal,
		Status:      status,
		Attempts:    0,
		MaxAttempts: 3,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func setupProcessor(prov *testProvider) (*worker.Processor, *websocket.Hub, *observability.MetricsCollector) {
	logger := slog.Default()
	hub := websocket.NewHub(logger)
	metrics := observability.NewMetricsCollector(hub.Len)
	retrier := retry.NewStrategy(10*time.Millisecond, 100*time.Millisecond)
	deadLetter := retry.NewDeadLetterHandler(pgContainer.Pool)
	repo := postgres.NewNotificationRepo(pgContainer.Pool)
	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client, 30*time.Second)

	proc := worker.NewProcessor(repo, prov, producer, consumer, retrier, deadLetter, hub, metrics, logger)
	return proc, hub, metrics
}

func TestProcessor_SuccessFlow(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pgContainer.TruncateTables(ctx))

	prov := &testProvider{msgID: "provider-abc-123"}
	proc, _, metrics := setupProcessor(prov)
	repo := postgres.NewNotificationRepo(pgContainer.Pool)

	// Create notification in DB
	n := newTestNotification(domain.ChannelSMS, domain.StatusQueued)
	require.NoError(t, repo.Create(ctx, n))

	// Process
	msg := queue.Message{
		NotificationID: n.ID,
		Channel:        n.Channel,
		Priority:       n.Priority,
	}
	proc.Process(ctx, msg)

	// Verify: status = sent, provider_msg_id set, sent_at set
	got, err := repo.GetByID(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusSent, got.Status)
	require.NotNil(t, got.ProviderMsgID)
	assert.Equal(t, "provider-abc-123", *got.ProviderMsgID)
	require.NotNil(t, got.SentAt)

	// Provider was called exactly once
	assert.Equal(t, 1, prov.callCount())

	// Metrics incremented
	snap := metrics.Snapshot()
	assert.Equal(t, int64(1), snap.Channels["sms"].Sent)
}

func TestProcessor_FailureAndRetry(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pgContainer.TruncateTables(ctx))

	prov := &testProvider{err: errors.New("provider timeout")}
	proc, _, metrics := setupProcessor(prov)
	repo := postgres.NewNotificationRepo(pgContainer.Pool)

	// Create notification in DB
	n := newTestNotification(domain.ChannelEmail, domain.StatusQueued)
	require.NoError(t, repo.Create(ctx, n))

	msg := queue.Message{
		NotificationID: n.ID,
		Channel:        n.Channel,
		Priority:       n.Priority,
	}
	proc.Process(ctx, msg)

	// Verify: status = queued (retry), attempts = 1, last_error set
	got, err := repo.GetByID(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusQueued, got.Status)
	assert.Equal(t, 1, got.Attempts)
	require.NotNil(t, got.LastError)
	assert.Contains(t, *got.LastError, "provider timeout")

	// Metrics: failed incremented
	snap := metrics.Snapshot()
	assert.Equal(t, int64(1), snap.Channels["email"].Failed)
}

func TestProcessor_MaxRetriesExhausted(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pgContainer.TruncateTables(ctx))

	prov := &testProvider{err: errors.New("persistent failure")}
	proc, _, _ := setupProcessor(prov)
	repo := postgres.NewNotificationRepo(pgContainer.Pool)

	// Create notification already at max-1 attempts
	n := newTestNotification(domain.ChannelPush, domain.StatusQueued)
	n.Attempts = 2 // MaxAttempts is 3, so next failure exhausts retries
	require.NoError(t, repo.Create(ctx, n))
	// Update attempts in DB
	require.NoError(t, repo.UpdateStatusWithError(ctx, n.ID, domain.StatusQueued, "prev error", 2))

	msg := queue.Message{
		NotificationID: n.ID,
		Channel:        n.Channel,
		Priority:       n.Priority,
	}
	proc.Process(ctx, msg)

	// Verify: status = failed
	got, err := repo.GetByID(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusFailed, got.Status)
	assert.Equal(t, 3, got.Attempts)
	require.NotNil(t, got.LastError)
	assert.Contains(t, *got.LastError, "persistent failure")

	// Verify: dead letter row exists
	var dlCount int
	err = pgContainer.Pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM dead_letters WHERE notification_id = $1", n.ID).Scan(&dlCount)
	require.NoError(t, err)
	assert.Equal(t, 1, dlCount)
}

func TestProcessor_FailureEnqueuesDelayedRetry(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pgContainer.TruncateTables(ctx))
	require.NoError(t, redisContainer.FlushAll(ctx))

	prov := &testProvider{err: errors.New("temporary failure")}
	proc, _, _ := setupProcessor(prov)
	repo := postgres.NewNotificationRepo(pgContainer.Pool)

	n := newTestNotification(domain.ChannelSMS, domain.StatusQueued)
	require.NoError(t, repo.Create(ctx, n))

	msg := queue.Message{
		NotificationID: n.ID,
		Channel:        n.Channel,
		Priority:       n.Priority,
	}
	proc.Process(ctx, msg)

	// Notification should be back to queued with attempts=1
	got, err := repo.GetByID(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusQueued, got.Status)
	assert.Equal(t, 1, got.Attempts)

	// A retry message should be in the delayed set
	count, err := redisContainer.Client.ZCard(ctx, "retry:delayed").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "failed notification should be in retry:delayed set")
}

func TestProcessor_CancelledSkipped(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pgContainer.TruncateTables(ctx))

	prov := &testProvider{msgID: "should-not-be-called"}
	proc, _, _ := setupProcessor(prov)
	repo := postgres.NewNotificationRepo(pgContainer.Pool)

	// Create cancelled notification
	n := newTestNotification(domain.ChannelSMS, domain.StatusCancelled)
	require.NoError(t, repo.Create(ctx, n))

	msg := queue.Message{
		NotificationID: n.ID,
		Channel:        n.Channel,
		Priority:       n.Priority,
	}
	proc.Process(ctx, msg)

	// Provider should NOT have been called
	assert.Equal(t, 0, prov.callCount())

	// Status unchanged
	got, err := repo.GetByID(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusCancelled, got.Status)
}

func TestProcessor_MultipleRetriesThenSuccess(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pgContainer.TruncateTables(ctx))

	// Provider fails twice, then succeeds
	prov := &countingProvider{
		failUntil: 2,
		msgID:     "success-after-retries",
	}
	logger := slog.Default()
	hub := websocket.NewHub(logger)
	metrics := observability.NewMetricsCollector(hub.Len)
	retrier := retry.NewStrategy(10*time.Millisecond, 100*time.Millisecond)
	deadLetter := retry.NewDeadLetterHandler(pgContainer.Pool)
	repo := postgres.NewNotificationRepo(pgContainer.Pool)
	producer := qredis.NewProducer(redisContainer.Client)
	consumer := qredis.NewConsumer(redisContainer.Client, 30*time.Second)
	proc := worker.NewProcessor(repo, prov, producer, consumer, retrier, deadLetter, hub, metrics, logger)

	n := newTestNotification(domain.ChannelSMS, domain.StatusQueued)
	require.NoError(t, repo.Create(ctx, n))

	msg := queue.Message{
		NotificationID: n.ID,
		Channel:        n.Channel,
		Priority:       n.Priority,
	}

	// First attempt: fails, status → queued with attempts=1
	proc.Process(ctx, msg)
	got, _ := repo.GetByID(ctx, n.ID)
	assert.Equal(t, domain.StatusQueued, got.Status)
	assert.Equal(t, 1, got.Attempts)

	// Second attempt: fails again, status → queued with attempts=2
	proc.Process(ctx, msg)
	got, _ = repo.GetByID(ctx, n.ID)
	assert.Equal(t, domain.StatusQueued, got.Status)
	assert.Equal(t, 2, got.Attempts)

	// Third attempt: succeeds
	proc.Process(ctx, msg)
	got, _ = repo.GetByID(ctx, n.ID)
	assert.Equal(t, domain.StatusSent, got.Status)
	require.NotNil(t, got.ProviderMsgID)
	assert.Equal(t, "success-after-retries", *got.ProviderMsgID)
}

// countingProvider fails for the first N calls, then succeeds.
type countingProvider struct {
	mu        sync.Mutex
	calls     int
	failUntil int
	msgID     string
}

func (p *countingProvider) Send(ctx context.Context, n *domain.Notification) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls <= p.failUntil {
		return "", errors.New("transient failure")
	}
	return p.msgID, nil
}

//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/repository"
	"github.com/insider/insider/internal/repository/postgres"
	"github.com/insider/insider/internal/testutil"
)

var pgContainer *testutil.PostgresContainer

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error
	pgContainer, err = testutil.SetupPostgres(ctx)
	if err != nil {
		panic("failed to setup postgres: " + err.Error())
	}
	defer pgContainer.Cleanup(ctx)
	m.Run()
}

func setupNotificationRepo(t *testing.T) *postgres.NotificationRepo {
	t.Helper()
	require.NoError(t, pgContainer.TruncateTables(context.Background()))
	return postgres.NewNotificationRepo(pgContainer.Pool)
}

func newNotification(channel domain.Channel) *domain.Notification {
	now := time.Now().Truncate(time.Microsecond)
	return &domain.Notification{
		ID:          uuid.New(),
		Recipient:   "+1234567890",
		Channel:     channel,
		Content:     "Hello World",
		Priority:    domain.PriorityNormal,
		Status:      domain.StatusPending,
		Attempts:    0,
		MaxAttempts: 5,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func TestNotificationRepo_CreateAndGetByID(t *testing.T) {
	repo := setupNotificationRepo(t)
	ctx := context.Background()

	n := newNotification(domain.ChannelSMS)
	n.TemplateVars = map[string]interface{}{"name": "John"}

	err := repo.Create(ctx, n)
	require.NoError(t, err)

	got, err := repo.GetByID(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, n.ID, got.ID)
	assert.Equal(t, n.Recipient, got.Recipient)
	assert.Equal(t, n.Channel, got.Channel)
	assert.Equal(t, n.Content, got.Content)
	assert.Equal(t, n.Priority, got.Priority)
	assert.Equal(t, n.Status, got.Status)
	assert.Equal(t, "John", got.TemplateVars["name"])
}

func TestNotificationRepo_GetByID_NotFound(t *testing.T) {
	repo := setupNotificationRepo(t)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, uuid.New())
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestNotificationRepo_DuplicateIdempotencyKey(t *testing.T) {
	repo := setupNotificationRepo(t)
	ctx := context.Background()

	key := "unique-key-123"
	n1 := newNotification(domain.ChannelSMS)
	n1.IdempotencyKey = &key

	n2 := newNotification(domain.ChannelEmail)
	n2.IdempotencyKey = &key

	require.NoError(t, repo.Create(ctx, n1))
	err := repo.Create(ctx, n2)
	assert.ErrorIs(t, err, domain.ErrDuplicateKey)
}

func TestNotificationRepo_CreateBatch(t *testing.T) {
	repo := setupNotificationRepo(t)
	ctx := context.Background()

	batchID := uuid.New()
	notifications := make([]*domain.Notification, 5)
	for i := range notifications {
		notifications[i] = newNotification(domain.ChannelEmail)
		notifications[i].BatchID = &batchID
	}

	err := repo.CreateBatch(ctx, notifications)
	require.NoError(t, err)

	got, err := repo.GetByBatchID(ctx, batchID)
	require.NoError(t, err)
	assert.Len(t, got, 5)
	for _, n := range got {
		assert.Equal(t, &batchID, n.BatchID)
	}
}

func TestNotificationRepo_UpdateStatus(t *testing.T) {
	repo := setupNotificationRepo(t)
	ctx := context.Background()

	n := newNotification(domain.ChannelPush)
	require.NoError(t, repo.Create(ctx, n))

	err := repo.UpdateStatus(ctx, n.ID, domain.StatusQueued)
	require.NoError(t, err)

	got, err := repo.GetByID(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusQueued, got.Status)
}

func TestNotificationRepo_UpdateStatusWithError(t *testing.T) {
	repo := setupNotificationRepo(t)
	ctx := context.Background()

	n := newNotification(domain.ChannelSMS)
	require.NoError(t, repo.Create(ctx, n))

	err := repo.UpdateStatusWithError(ctx, n.ID, domain.StatusFailed, "provider timeout", 3)
	require.NoError(t, err)

	got, err := repo.GetByID(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusFailed, got.Status)
	require.NotNil(t, got.LastError)
	assert.Equal(t, "provider timeout", *got.LastError)
	assert.Equal(t, 3, got.Attempts)
}

func TestNotificationRepo_UpdateSent(t *testing.T) {
	repo := setupNotificationRepo(t)
	ctx := context.Background()

	n := newNotification(domain.ChannelEmail)
	require.NoError(t, repo.Create(ctx, n))

	providerID := "provider-msg-123"
	err := repo.UpdateSent(ctx, n.ID, providerID)
	require.NoError(t, err)

	got, err := repo.GetByID(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusSent, got.Status)
	require.NotNil(t, got.ProviderMsgID)
	assert.Equal(t, providerID, *got.ProviderMsgID)
	require.NotNil(t, got.SentAt)
}

func TestNotificationRepo_List(t *testing.T) {
	repo := setupNotificationRepo(t)
	ctx := context.Background()

	// Create notifications across channels
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.Create(ctx, newNotification(domain.ChannelSMS)))
	}
	for i := 0; i < 2; i++ {
		require.NoError(t, repo.Create(ctx, newNotification(domain.ChannelEmail)))
	}

	t.Run("no filter", func(t *testing.T) {
		results, total, err := repo.List(ctx, repository.NotificationFilter{Limit: 10})
		require.NoError(t, err)
		assert.Equal(t, 5, total)
		assert.Len(t, results, 5)
	})

	t.Run("filter by channel", func(t *testing.T) {
		ch := domain.ChannelSMS
		results, total, err := repo.List(ctx, repository.NotificationFilter{
			Channel: &ch,
			Limit:   10,
		})
		require.NoError(t, err)
		assert.Equal(t, 3, total)
		assert.Len(t, results, 3)
	})

	t.Run("filter by status", func(t *testing.T) {
		st := domain.StatusPending
		results, total, err := repo.List(ctx, repository.NotificationFilter{
			Status: &st,
			Limit:  10,
		})
		require.NoError(t, err)
		assert.Equal(t, 5, total)
		assert.Len(t, results, 5)
	})

	t.Run("pagination", func(t *testing.T) {
		results, total, err := repo.List(ctx, repository.NotificationFilter{
			Limit:  2,
			Offset: 0,
		})
		require.NoError(t, err)
		assert.Equal(t, 5, total)
		assert.Len(t, results, 2)

		results2, _, err := repo.List(ctx, repository.NotificationFilter{
			Limit:  2,
			Offset: 2,
		})
		require.NoError(t, err)
		assert.Len(t, results2, 2)
		assert.NotEqual(t, results[0].ID, results2[0].ID)
	})
}

func TestNotificationRepo_GetPendingScheduled(t *testing.T) {
	repo := setupNotificationRepo(t)
	ctx := context.Background()

	// Create a scheduled notification in the past (should be picked up)
	past := time.Now().Add(-1 * time.Minute).Truncate(time.Microsecond)
	n1 := newNotification(domain.ChannelSMS)
	n1.Status = domain.StatusScheduled
	n1.ScheduledAt = &past
	require.NoError(t, repo.Create(ctx, n1))

	// Create a scheduled notification in the future (should NOT be picked up)
	future := time.Now().Add(1 * time.Hour).Truncate(time.Microsecond)
	n2 := newNotification(domain.ChannelEmail)
	n2.Status = domain.StatusScheduled
	n2.ScheduledAt = &future
	require.NoError(t, repo.Create(ctx, n2))

	// Create a non-scheduled notification
	n3 := newNotification(domain.ChannelPush)
	require.NoError(t, repo.Create(ctx, n3))

	results, err := repo.GetPendingScheduled(ctx)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, n1.ID, results[0].ID)
}

func TestNotificationRepo_GetPendingForRecovery(t *testing.T) {
	repo := setupNotificationRepo(t)
	ctx := context.Background()

	// Pending notification — should be recovered
	n1 := newNotification(domain.ChannelSMS)
	n1.Status = domain.StatusPending
	require.NoError(t, repo.Create(ctx, n1))

	// Queued notification — should be recovered
	n2 := newNotification(domain.ChannelEmail)
	n2.Status = domain.StatusQueued
	require.NoError(t, repo.Create(ctx, n2))

	// Sent notification — should NOT be recovered
	n3 := newNotification(domain.ChannelPush)
	n3.Status = domain.StatusSent
	require.NoError(t, repo.Create(ctx, n3))

	results, err := repo.GetPendingForRecovery(ctx)
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

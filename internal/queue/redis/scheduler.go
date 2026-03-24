package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/queue"
	"github.com/insider/insider/internal/repository"
)

const schedulerLockKey = "scheduler:lock"
const schedulerLockTTL = 5 * time.Second

type Scheduler struct {
	repo     repository.NotificationRepository
	producer queue.Producer
	redis    *goredis.Client
	logger   *slog.Logger
	interval time.Duration
}

func NewScheduler(repo repository.NotificationRepository, producer queue.Producer, redisClient *goredis.Client, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		repo:     repo,
		producer: producer,
		redis:    redisClient,
		logger:   logger,
		interval: time.Second,
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.logger.Info("scheduler started")

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler stopped")
			return
		case <-ticker.C:
			s.poll(ctx)
		}
	}
}

func (s *Scheduler) poll(ctx context.Context) {
	// Acquire distributed lock so only one scheduler instance processes at a time
	acquired, err := s.redis.SetNX(ctx, schedulerLockKey, "1", schedulerLockTTL).Result() //nolint:staticcheck // SetNX is clearer for lock semantics
	if err != nil {
		s.logger.Error("scheduler lock acquisition failed", "error", err)
		return
	}
	if !acquired {
		return
	}

	// Process in batches until no more pending scheduled notifications
	for {
		notifications, err := s.repo.GetPendingScheduled(ctx)
		if err != nil {
			s.logger.Error("scheduler poll failed", "error", err)
			break
		}

		if len(notifications) == 0 {
			break
		}

		for _, n := range notifications {
			msg := queue.Message{
				NotificationID: n.ID,
				Channel:        n.Channel,
				Priority:       n.Priority,
			}

			if err := s.producer.Enqueue(ctx, msg); err != nil {
				s.logger.Error("scheduler enqueue failed", "error", err, "id", n.ID)
				continue
			}

			if err := s.repo.UpdateStatus(ctx, n.ID, domain.StatusQueued); err != nil {
				s.logger.Error("scheduler status update failed", "error", err, "id", n.ID)
			}
		}
	}

	s.promoteDelayedRetries(ctx)
	s.reclaimExpiredProcessing(ctx)
}

// reclaimExpiredProcessing moves messages that exceeded their visibility timeout
// back to the main queue. This replaces the startup-only recovery sweep with
// continuous crash recovery.
func (s *Scheduler) reclaimExpiredProcessing(ctx context.Context) {
	now := fmt.Sprintf("%d", time.Now().UnixNano())
	channels := []domain.Channel{domain.ChannelSMS, domain.ChannelEmail, domain.ChannelPush}

	for _, ch := range channels {
		procKey := processingPrefix + string(ch)
		results, err := s.redis.ZRangeArgs(ctx, goredis.ZRangeArgs{
			Key:     procKey,
			Start:   "-inf",
			Stop:    now,
			ByScore: true,
		}).Result()
		if err != nil {
			s.logger.Error("reclaim expired processing failed", "error", err, "channel", ch)
			continue
		}

		for _, member := range results {
			var msg queue.Message
			if err := json.Unmarshal([]byte(member), &msg); err != nil {
				s.logger.Error("unmarshal processing message", "error", err)
				continue
			}

			if err := s.producer.Enqueue(ctx, msg); err != nil {
				s.logger.Error("reclaim enqueue failed", "error", err, "id", msg.NotificationID)
				continue
			}

			s.redis.ZRem(ctx, procKey, member)
			s.logger.Info("reclaimed expired message", "id", msg.NotificationID, "channel", ch)
		}
	}
}

func (s *Scheduler) promoteDelayedRetries(ctx context.Context) {
	now := fmt.Sprintf("%d", time.Now().UnixNano())

	results, err := s.redis.ZRangeArgs(ctx, goredis.ZRangeArgs{
		Key:     retryDelayedKey,
		Start:   "-inf",
		Stop:    now,
		ByScore: true,
	}).Result()
	if err != nil {
		s.logger.Error("poll delayed retries failed", "error", err)
		return
	}

	for _, member := range results {
		var msg queue.Message
		if err := json.Unmarshal([]byte(member), &msg); err != nil {
			s.logger.Error("unmarshal delayed retry", "error", err)
			continue
		}

		if err := s.producer.Enqueue(ctx, msg); err != nil {
			s.logger.Error("promote delayed retry failed", "error", err, "id", msg.NotificationID)
			continue
		}

		s.redis.ZRem(ctx, retryDelayedKey, member)
	}
}

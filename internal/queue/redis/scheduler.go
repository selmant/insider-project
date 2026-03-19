package redis

import (
	"context"
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
	acquired, err := s.redis.SetNX(ctx, schedulerLockKey, "1", schedulerLockTTL).Result()
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
			return
		}

		if len(notifications) == 0 {
			return
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
}

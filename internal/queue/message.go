package queue

import (
	"time"

	"github.com/google/uuid"
	"github.com/insider/insider/internal/domain"
)

type Message struct {
	NotificationID uuid.UUID       `json:"notification_id"`
	Channel        domain.Channel  `json:"channel"`
	Priority       domain.Priority `json:"priority"`
	ScheduledAt    *time.Time      `json:"scheduled_at,omitempty"`
}

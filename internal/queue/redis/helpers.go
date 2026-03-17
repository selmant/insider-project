package redis

import (
	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/queue"
)

func MessageFromNotification(n *domain.Notification) queue.Message {
	return queue.Message{
		NotificationID: n.ID,
		Channel:        n.Channel,
		Priority:       n.Priority,
		ScheduledAt:    n.ScheduledAt,
	}
}

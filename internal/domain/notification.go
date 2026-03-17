package domain

import (
	"time"

	"github.com/google/uuid"
)

type Channel string

const (
	ChannelSMS   Channel = "sms"
	ChannelEmail Channel = "email"
	ChannelPush  Channel = "push"
)

func (c Channel) Valid() bool {
	switch c {
	case ChannelSMS, ChannelEmail, ChannelPush:
		return true
	}
	return false
}

type Priority string

const (
	PriorityHigh   Priority = "high"
	PriorityNormal Priority = "normal"
	PriorityLow    Priority = "low"
)

func (p Priority) Valid() bool {
	switch p {
	case PriorityHigh, PriorityNormal, PriorityLow:
		return true
	}
	return false
}

func (p Priority) Score() int {
	switch p {
	case PriorityHigh:
		return 0
	case PriorityNormal:
		return 1
	case PriorityLow:
		return 2
	}
	return 1
}

type Status string

const (
	StatusPending    Status = "pending"
	StatusScheduled  Status = "scheduled"
	StatusQueued     Status = "queued"
	StatusProcessing Status = "processing"
	StatusSent       Status = "sent"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
)

type Notification struct {
	ID             uuid.UUID              `json:"id"`
	BatchID        *uuid.UUID             `json:"batch_id,omitempty"`
	IdempotencyKey *string                `json:"idempotency_key,omitempty"`
	Recipient      string                 `json:"recipient"`
	Channel        Channel                `json:"channel"`
	Content        string                 `json:"content"`
	Priority       Priority               `json:"priority"`
	Status         Status                 `json:"status"`
	TemplateID     *uuid.UUID             `json:"template_id,omitempty"`
	TemplateVars   map[string]interface{} `json:"template_vars,omitempty"`
	ScheduledAt    *time.Time             `json:"scheduled_at,omitempty"`
	ProviderMsgID  *string                `json:"provider_msg_id,omitempty"`
	Attempts       int                    `json:"attempts"`
	MaxAttempts    int                    `json:"max_attempts"`
	LastError      *string                `json:"last_error,omitempty"`
	SentAt         *time.Time             `json:"sent_at,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
}

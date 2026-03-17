package domain

import "errors"

var (
	ErrNotFound           = errors.New("not found")
	ErrDuplicateKey       = errors.New("duplicate idempotency key")
	ErrInvalidChannel     = errors.New("invalid channel: must be sms, email, or push")
	ErrInvalidPriority    = errors.New("invalid priority: must be high, normal, or low")
	ErrInvalidRecipient   = errors.New("recipient is required")
	ErrInvalidContent     = errors.New("content is required")
	ErrNotCancellable     = errors.New("notification cannot be cancelled in current status")
	ErrBatchTooLarge      = errors.New("batch size exceeds maximum of 1000")
	ErrTemplateNotFound   = errors.New("template not found")
	ErrTemplateMissingVar = errors.New("template variable missing")
	ErrRateLimited        = errors.New("rate limited")
	ErrCircuitOpen        = errors.New("circuit breaker is open")
)

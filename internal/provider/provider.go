package provider

import (
	"context"

	"github.com/insider/insider/internal/domain"
)

type Provider interface {
	Send(ctx context.Context, n *domain.Notification) (providerMsgID string, err error)
}

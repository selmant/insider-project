package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/insider/insider/internal/domain"
)

type TemplateRepository interface {
	Create(ctx context.Context, t *domain.Template) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Template, error)
	GetByName(ctx context.Context, name string) (*domain.Template, error)
	Update(ctx context.Context, t *domain.Template) error
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context) ([]*domain.Template, error)
}

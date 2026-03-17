package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/insider/insider/internal/domain"
)

type TemplateRepo struct {
	pool *pgxpool.Pool
}

func NewTemplateRepo(pool *pgxpool.Pool) *TemplateRepo {
	return &TemplateRepo{pool: pool}
}

func (r *TemplateRepo) Create(ctx context.Context, t *domain.Template) error {
	varsJSON, err := json.Marshal(t.Variables)
	if err != nil {
		return fmt.Errorf("marshal variables: %w", err)
	}
	_, err = r.pool.Exec(ctx, insertTemplate,
		t.ID, t.Name, t.Channel, t.Content, varsJSON, t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert template: %w", err)
	}
	return nil
}

func (r *TemplateRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Template, error) {
	t := &domain.Template{}
	var varsJSON []byte
	err := r.pool.QueryRow(ctx, selectTemplateByID, id).Scan(
		&t.ID, &t.Name, &t.Channel, &t.Content, &varsJSON, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get template: %w", err)
	}
	if err := json.Unmarshal(varsJSON, &t.Variables); err != nil {
		return nil, fmt.Errorf("unmarshal variables: %w", err)
	}
	return t, nil
}

func (r *TemplateRepo) GetByName(ctx context.Context, name string) (*domain.Template, error) {
	t := &domain.Template{}
	var varsJSON []byte
	err := r.pool.QueryRow(ctx, selectTemplateByName, name).Scan(
		&t.ID, &t.Name, &t.Channel, &t.Content, &varsJSON, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get template by name: %w", err)
	}
	if err := json.Unmarshal(varsJSON, &t.Variables); err != nil {
		return nil, fmt.Errorf("unmarshal variables: %w", err)
	}
	return t, nil
}

func (r *TemplateRepo) Update(ctx context.Context, t *domain.Template) error {
	varsJSON, err := json.Marshal(t.Variables)
	if err != nil {
		return fmt.Errorf("marshal variables: %w", err)
	}
	_, err = r.pool.Exec(ctx, updateTemplate, t.ID, t.Name, t.Channel, t.Content, varsJSON)
	if err != nil {
		return fmt.Errorf("update template: %w", err)
	}
	return nil
}

func (r *TemplateRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, deleteTemplate, id)
	return err
}

func (r *TemplateRepo) List(ctx context.Context) ([]*domain.Template, error) {
	rows, err := r.pool.Query(ctx, selectAllTemplates)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	defer rows.Close()

	var templates []*domain.Template
	for rows.Next() {
		t := &domain.Template{}
		var varsJSON []byte
		if err := rows.Scan(&t.ID, &t.Name, &t.Channel, &t.Content, &varsJSON, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan template: %w", err)
		}
		if err := json.Unmarshal(varsJSON, &t.Variables); err != nil {
			return nil, fmt.Errorf("unmarshal variables: %w", err)
		}
		templates = append(templates, t)
	}
	return templates, rows.Err()
}

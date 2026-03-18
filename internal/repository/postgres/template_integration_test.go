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
	"github.com/insider/insider/internal/repository/postgres"
)

func setupTemplateRepo(t *testing.T) *postgres.TemplateRepo {
	t.Helper()
	require.NoError(t, pgContainer.TruncateTables(context.Background()))
	return postgres.NewTemplateRepo(pgContainer.Pool)
}

func newTemplate(channel domain.Channel) *domain.Template {
	now := time.Now().Truncate(time.Microsecond)
	return &domain.Template{
		ID:        uuid.New(),
		Name:      "template-" + uuid.NewString()[:8],
		Channel:   channel,
		Content:   "Hello {{.Name}}, your code is {{.Code}}",
		Variables: []string{"Name", "Code"},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestTemplateRepo_CreateAndGetByID(t *testing.T) {
	repo := setupTemplateRepo(t)
	ctx := context.Background()

	tmpl := newTemplate(domain.ChannelSMS)
	require.NoError(t, repo.Create(ctx, tmpl))

	got, err := repo.GetByID(ctx, tmpl.ID)
	require.NoError(t, err)
	assert.Equal(t, tmpl.ID, got.ID)
	assert.Equal(t, tmpl.Name, got.Name)
	assert.Equal(t, tmpl.Channel, got.Channel)
	assert.Equal(t, tmpl.Content, got.Content)
	assert.Equal(t, tmpl.Variables, got.Variables)
}

func TestTemplateRepo_GetByID_NotFound(t *testing.T) {
	repo := setupTemplateRepo(t)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, uuid.New())
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestTemplateRepo_GetByName(t *testing.T) {
	repo := setupTemplateRepo(t)
	ctx := context.Background()

	tmpl := newTemplate(domain.ChannelEmail)
	require.NoError(t, repo.Create(ctx, tmpl))

	got, err := repo.GetByName(ctx, tmpl.Name)
	require.NoError(t, err)
	assert.Equal(t, tmpl.ID, got.ID)
}

func TestTemplateRepo_Update(t *testing.T) {
	repo := setupTemplateRepo(t)
	ctx := context.Background()

	tmpl := newTemplate(domain.ChannelPush)
	require.NoError(t, repo.Create(ctx, tmpl))

	tmpl.Content = "Updated content: {{.Name}}"
	tmpl.Variables = []string{"Name"}
	require.NoError(t, repo.Update(ctx, tmpl))

	got, err := repo.GetByID(ctx, tmpl.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated content: {{.Name}}", got.Content)
	assert.Equal(t, []string{"Name"}, got.Variables)
}

func TestTemplateRepo_Delete(t *testing.T) {
	repo := setupTemplateRepo(t)
	ctx := context.Background()

	tmpl := newTemplate(domain.ChannelSMS)
	require.NoError(t, repo.Create(ctx, tmpl))

	require.NoError(t, repo.Delete(ctx, tmpl.ID))

	_, err := repo.GetByID(ctx, tmpl.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestTemplateRepo_List(t *testing.T) {
	repo := setupTemplateRepo(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		require.NoError(t, repo.Create(ctx, newTemplate(domain.ChannelEmail)))
	}

	templates, err := repo.List(ctx)
	require.NoError(t, err)
	assert.Len(t, templates, 3)
}

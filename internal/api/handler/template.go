package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/repository"
)

type TemplateHandler struct {
	repo   repository.TemplateRepository
	logger *slog.Logger
}

func NewTemplateHandler(repo repository.TemplateRepository, logger *slog.Logger) *TemplateHandler {
	return &TemplateHandler{repo: repo, logger: logger}
}

type CreateTemplateRequest struct {
	Name      string         `json:"name"`
	Channel   domain.Channel `json:"channel"`
	Content   string         `json:"content"`
	Variables []string       `json:"variables"`
}

func (h *TemplateHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" || req.Content == "" || !req.Channel.Valid() {
		writeError(w, http.StatusBadRequest, "name, content, and valid channel are required")
		return
	}

	now := time.Now()
	t := &domain.Template{
		ID:        uuid.New(),
		Name:      req.Name,
		Channel:   req.Channel,
		Content:   req.Content,
		Variables: req.Variables,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := h.repo.Create(r.Context(), t); err != nil {
		h.logger.Error("create template", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create template")
		return
	}

	writeJSON(w, http.StatusCreated, t)
}

func (h *TemplateHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid template ID")
		return
	}

	t, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "template not found")
			return
		}
		h.logger.Error("get template", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get template")
		return
	}

	writeJSON(w, http.StatusOK, t)
}

func (h *TemplateHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid template ID")
		return
	}

	var req CreateTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	t, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "template not found")
			return
		}
		h.logger.Error("get template for update", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get template")
		return
	}

	if req.Name != "" {
		t.Name = req.Name
	}
	if req.Channel.Valid() {
		t.Channel = req.Channel
	}
	if req.Content != "" {
		t.Content = req.Content
	}
	if req.Variables != nil {
		t.Variables = req.Variables
	}

	if err := h.repo.Update(r.Context(), t); err != nil {
		h.logger.Error("update template", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update template")
		return
	}

	writeJSON(w, http.StatusOK, t)
}

func (h *TemplateHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid template ID")
		return
	}

	if err := h.repo.Delete(r.Context(), id); err != nil {
		h.logger.Error("delete template", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete template")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *TemplateHandler) List(w http.ResponseWriter, r *http.Request) {
	templates, err := h.repo.List(r.Context())
	if err != nil {
		h.logger.Error("list templates", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list templates")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"templates": templates,
	})
}

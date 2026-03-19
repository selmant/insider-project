package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/insider/insider/internal/domain"
	"github.com/insider/insider/internal/queue"
	"github.com/insider/insider/internal/repository"
	"github.com/insider/insider/internal/template"
)

type NotificationHandler struct {
	repo     repository.NotificationRepository
	tmplRepo repository.TemplateRepository
	producer queue.Producer
	engine   *template.Engine
	logger   *slog.Logger
}

func NewNotificationHandler(
	repo repository.NotificationRepository,
	tmplRepo repository.TemplateRepository,
	producer queue.Producer,
	engine *template.Engine,
	logger *slog.Logger,
) *NotificationHandler {
	return &NotificationHandler{
		repo:     repo,
		tmplRepo: tmplRepo,
		producer: producer,
		engine:   engine,
		logger:   logger,
	}
}

type CreateNotificationRequest struct {
	Recipient      string                 `json:"recipient"`
	Channel        domain.Channel         `json:"channel"`
	Content        string                 `json:"content,omitempty"`
	Priority       domain.Priority        `json:"priority,omitempty"`
	TemplateID     *uuid.UUID             `json:"template_id,omitempty"`
	TemplateVars   map[string]interface{} `json:"template_vars,omitempty"`
	ScheduledAt    *time.Time             `json:"scheduled_at,omitempty"`
	IdempotencyKey *string                `json:"idempotency_key,omitempty"`
}

type BatchCreateRequest struct {
	Notifications []CreateNotificationRequest `json:"notifications"`
}

func (h *NotificationHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateNotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	n, err := h.buildNotification(r, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.repo.Create(r.Context(), n); err != nil {
		if errors.Is(err, domain.ErrDuplicateKey) {
			writeError(w, http.StatusConflict, "duplicate idempotency key")
			return
		}
		h.logger.Error("create notification", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create notification")
		return
	}

	if err := h.enqueue(r, n); err != nil {
		h.logger.Error("enqueue notification", "error", err)
	}

	writeJSON(w, http.StatusCreated, n)
}

func (h *NotificationHandler) BatchCreate(w http.ResponseWriter, r *http.Request) {
	var req BatchCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Notifications) > 1000 {
		writeError(w, http.StatusBadRequest, domain.ErrBatchTooLarge.Error())
		return
	}

	if len(req.Notifications) == 0 {
		writeError(w, http.StatusBadRequest, "at least one notification is required")
		return
	}

	batchID := uuid.New()
	var notifications []*domain.Notification

	for _, nr := range req.Notifications {
		n, err := h.buildNotification(r, nr)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		n.BatchID = &batchID
		notifications = append(notifications, n)
	}

	if err := h.repo.CreateBatch(r.Context(), notifications); err != nil {
		if errors.Is(err, domain.ErrDuplicateKey) {
			writeError(w, http.StatusConflict, "duplicate idempotency key in batch")
			return
		}
		h.logger.Error("batch create", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create batch")
		return
	}

	if err := h.batchEnqueue(r, notifications); err != nil {
		h.logger.Error("batch enqueue notifications", "error", err)
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"batch_id": batchID,
		"count":    len(notifications),
	})
}

func (h *NotificationHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid notification ID")
		return
	}

	n, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "notification not found")
			return
		}
		h.logger.Error("get notification", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get notification")
		return
	}

	writeJSON(w, http.StatusOK, n)
}

func (h *NotificationHandler) GetBatch(w http.ResponseWriter, r *http.Request) {
	batchID, err := uuid.Parse(r.PathValue("batch_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid batch ID")
		return
	}

	notifications, err := h.repo.GetByBatchID(r.Context(), batchID)
	if err != nil {
		h.logger.Error("get batch", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get batch")
		return
	}

	statusCounts := make(map[domain.Status]int)
	for _, n := range notifications {
		statusCounts[n.Status]++
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"batch_id":      batchID,
		"total":         len(notifications),
		"status_counts": statusCounts,
		"notifications": notifications,
	})
}

func (h *NotificationHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid notification ID")
		return
	}

	n, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "notification not found")
			return
		}
		h.logger.Error("get notification for cancel", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get notification")
		return
	}

	switch n.Status {
	case domain.StatusPending, domain.StatusScheduled, domain.StatusQueued:
		if err := h.repo.UpdateStatus(r.Context(), id, domain.StatusCancelled); err != nil {
			h.logger.Error("cancel notification", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to cancel notification")
			return
		}
		n.Status = domain.StatusCancelled
		writeJSON(w, http.StatusOK, n)
	default:
		writeError(w, http.StatusConflict, domain.ErrNotCancellable.Error())
	}
}

func (h *NotificationHandler) List(w http.ResponseWriter, r *http.Request) {
	filter := repository.NotificationFilter{
		Limit:  20,
		Offset: 0,
	}

	if v := r.URL.Query().Get("channel"); v != "" {
		ch := domain.Channel(v)
		filter.Channel = &ch
	}
	if v := r.URL.Query().Get("status"); v != "" {
		st := domain.Status(v)
		filter.Status = &st
	}
	if v := r.URL.Query().Get("priority"); v != "" {
		pr := domain.Priority(v)
		filter.Priority = &pr
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil && l > 0 && l <= 100 {
			filter.Limit = l
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if o, err := strconv.Atoi(v); err == nil && o >= 0 {
			filter.Offset = o
		}
	}
	if v := r.URL.Query().Get("cursor"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err == nil {
			filter.Cursor = &t
		}
	}

	notifications, total, err := h.repo.List(r.Context(), filter)
	if err != nil {
		h.logger.Error("list notifications", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list notifications")
		return
	}

	resp := map[string]interface{}{
		"notifications": notifications,
		"total":         total,
		"limit":         filter.Limit,
		"offset":        filter.Offset,
	}
	if len(notifications) > 0 {
		resp["next_cursor"] = notifications[len(notifications)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *NotificationHandler) buildNotification(r *http.Request, req CreateNotificationRequest) (*domain.Notification, error) {
	if req.Recipient == "" {
		return nil, domain.ErrInvalidRecipient
	}
	if !req.Channel.Valid() {
		return nil, domain.ErrInvalidChannel
	}
	if req.Priority == "" {
		req.Priority = domain.PriorityNormal
	}
	if !req.Priority.Valid() {
		return nil, domain.ErrInvalidPriority
	}

	content := req.Content

	// Render template if template_id is provided
	if req.TemplateID != nil {
		tmpl, err := h.tmplRepo.GetByID(r.Context(), *req.TemplateID)
		if err != nil {
			return nil, domain.ErrTemplateNotFound
		}
		rendered, err := h.engine.Render(tmpl.Name, tmpl.Content, req.TemplateVars)
		if err != nil {
			return nil, err
		}
		content = rendered
	}

	if content == "" {
		return nil, domain.ErrInvalidContent
	}

	now := time.Now()
	status := domain.StatusPending
	if req.ScheduledAt != nil {
		status = domain.StatusScheduled
	}

	return &domain.Notification{
		ID:             uuid.New(),
		IdempotencyKey: req.IdempotencyKey,
		Recipient:      req.Recipient,
		Channel:        req.Channel,
		Content:        content,
		Priority:       req.Priority,
		Status:         status,
		TemplateID:     req.TemplateID,
		TemplateVars:   req.TemplateVars,
		ScheduledAt:    req.ScheduledAt,
		Attempts:       0,
		MaxAttempts:    5,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

func (h *NotificationHandler) batchEnqueue(r *http.Request, notifications []*domain.Notification) error {
	var msgs []queue.Message
	var toUpdate []uuid.UUID

	for _, n := range notifications {
		if n.Status == domain.StatusScheduled {
			continue // Scheduler will pick it up
		}

		msgs = append(msgs, queue.Message{
			NotificationID: n.ID,
			Channel:        n.Channel,
			Priority:       n.Priority,
			ScheduledAt:    n.ScheduledAt,
		})
		toUpdate = append(toUpdate, n.ID)
	}

	if len(msgs) == 0 {
		return nil
	}

	if err := h.producer.EnqueueBatch(r.Context(), msgs); err != nil {
		return err
	}

	// Update statuses to queued in a single batch call
	if err := h.repo.UpdateBatchStatus(r.Context(), toUpdate, domain.StatusQueued); err != nil {
		h.logger.Error("update batch queued status", "error", err)
	}

	return nil
}

func (h *NotificationHandler) enqueue(r *http.Request, n *domain.Notification) error {
	if n.Status == domain.StatusScheduled {
		return nil // Scheduler will pick it up
	}

	msg := queue.Message{
		NotificationID: n.ID,
		Channel:        n.Channel,
		Priority:       n.Priority,
		ScheduledAt:    n.ScheduledAt,
	}

	if err := h.producer.Enqueue(r.Context(), msg); err != nil {
		return err
	}

	return h.repo.UpdateStatus(r.Context(), n.ID, domain.StatusQueued)
}

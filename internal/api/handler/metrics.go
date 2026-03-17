package handler

import (
	"net/http"

	"github.com/insider/insider/internal/observability"
)

type MetricsHandler struct {
	collector *observability.MetricsCollector
}

func NewMetricsHandler(collector *observability.MetricsCollector) *MetricsHandler {
	return &MetricsHandler{collector: collector}
}

func (h *MetricsHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.collector.Snapshot())
}

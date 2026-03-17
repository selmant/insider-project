package observability

import (
	"sync"
	"sync/atomic"

	"github.com/insider/insider/internal/domain"
)

type ChannelMetrics struct {
	Sent       int64 `json:"sent"`
	Failed     int64 `json:"failed"`
	Processing int64 `json:"processing"`
}

type MetricsSnapshot struct {
	Channels    map[string]*ChannelMetrics `json:"channels"`
	Connections int                        `json:"websocket_connections"`
}

type MetricsCollector struct {
	mu       sync.RWMutex
	channels map[domain.Channel]*ChannelMetrics
	connFunc func() int // returns current WebSocket connection count
}

func NewMetricsCollector(connFunc func() int) *MetricsCollector {
	return &MetricsCollector{
		channels: map[domain.Channel]*ChannelMetrics{
			domain.ChannelSMS:   {},
			domain.ChannelEmail: {},
			domain.ChannelPush:  {},
		},
		connFunc: connFunc,
	}
}

func (m *MetricsCollector) IncrSent(ch domain.Channel) {
	atomic.AddInt64(&m.channels[ch].Sent, 1)
}

func (m *MetricsCollector) IncrFailed(ch domain.Channel) {
	atomic.AddInt64(&m.channels[ch].Failed, 1)
}

func (m *MetricsCollector) IncrProcessing(ch domain.Channel) {
	atomic.AddInt64(&m.channels[ch].Processing, 1)
}

func (m *MetricsCollector) Snapshot() MetricsSnapshot {
	snap := MetricsSnapshot{
		Channels: make(map[string]*ChannelMetrics),
	}

	for ch, metrics := range m.channels {
		snap.Channels[string(ch)] = &ChannelMetrics{
			Sent:       atomic.LoadInt64(&metrics.Sent),
			Failed:     atomic.LoadInt64(&metrics.Failed),
			Processing: atomic.LoadInt64(&metrics.Processing),
		}
	}

	if m.connFunc != nil {
		snap.Connections = m.connFunc()
	}

	return snap
}

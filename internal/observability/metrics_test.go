package observability

import (
	"testing"

	"github.com/insider/insider/internal/domain"
)

func TestMetricsCollector(t *testing.T) {
	mc := NewMetricsCollector(func() int { return 5 })

	mc.IncrSent(domain.ChannelSMS)
	mc.IncrSent(domain.ChannelSMS)
	mc.IncrFailed(domain.ChannelEmail)
	mc.IncrProcessing(domain.ChannelPush)

	snap := mc.Snapshot()

	if snap.Channels["sms"].Sent != 2 {
		t.Errorf("sms sent = %d, want 2", snap.Channels["sms"].Sent)
	}
	if snap.Channels["email"].Failed != 1 {
		t.Errorf("email failed = %d, want 1", snap.Channels["email"].Failed)
	}
	if snap.Channels["push"].Processing != 1 {
		t.Errorf("push processing = %d, want 1", snap.Channels["push"].Processing)
	}
	if snap.Connections != 5 {
		t.Errorf("connections = %d, want 5", snap.Connections)
	}
}

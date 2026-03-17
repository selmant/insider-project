package domain

import "testing"

func TestChannel_Valid(t *testing.T) {
	tests := []struct {
		channel Channel
		want    bool
	}{
		{ChannelSMS, true},
		{ChannelEmail, true},
		{ChannelPush, true},
		{Channel("fax"), false},
		{Channel(""), false},
	}

	for _, tt := range tests {
		if got := tt.channel.Valid(); got != tt.want {
			t.Errorf("Channel(%q).Valid() = %v, want %v", tt.channel, got, tt.want)
		}
	}
}

func TestPriority_Valid(t *testing.T) {
	tests := []struct {
		priority Priority
		want     bool
	}{
		{PriorityHigh, true},
		{PriorityNormal, true},
		{PriorityLow, true},
		{Priority("urgent"), false},
		{Priority(""), false},
	}

	for _, tt := range tests {
		if got := tt.priority.Valid(); got != tt.want {
			t.Errorf("Priority(%q).Valid() = %v, want %v", tt.priority, got, tt.want)
		}
	}
}

func TestPriority_Score(t *testing.T) {
	tests := []struct {
		priority Priority
		want     int
	}{
		{PriorityHigh, 0},
		{PriorityNormal, 1},
		{PriorityLow, 2},
	}

	for _, tt := range tests {
		if got := tt.priority.Score(); got != tt.want {
			t.Errorf("Priority(%q).Score() = %v, want %v", tt.priority, got, tt.want)
		}
	}

	// High priority should have lower score than normal
	if PriorityHigh.Score() >= PriorityNormal.Score() {
		t.Error("high priority should have lower score than normal")
	}
}

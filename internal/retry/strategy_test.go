package retry

import (
	"testing"
	"time"
)

func TestStrategy_NextDelay(t *testing.T) {
	s := NewStrategy(time.Second, 5*time.Minute)

	// First attempt should be around 1s
	d1 := s.NextDelay(1)
	if d1 < 750*time.Millisecond || d1 > 1250*time.Millisecond {
		t.Errorf("attempt 1: got %v, want ~1s", d1)
	}

	// Second attempt should be around 2s
	d2 := s.NextDelay(2)
	if d2 < 1500*time.Millisecond || d2 > 2500*time.Millisecond {
		t.Errorf("attempt 2: got %v, want ~2s", d2)
	}

	// Third attempt should be around 4s
	d3 := s.NextDelay(3)
	if d3 < 3*time.Second || d3 > 5*time.Second {
		t.Errorf("attempt 3: got %v, want ~4s", d3)
	}

	// Should not exceed maxWait
	d10 := s.NextDelay(20)
	if d10 > 5*time.Minute+2*time.Minute { // with jitter
		t.Errorf("attempt 20: got %v, should not greatly exceed max 5m", d10)
	}
}

func TestStrategy_ExponentialGrowth(t *testing.T) {
	s := NewStrategy(time.Second, time.Hour)

	var prev time.Duration
	for i := 1; i <= 5; i++ {
		d := s.NextDelay(i)
		if i > 1 && d < prev/2 {
			t.Errorf("attempt %d (%v) should be roughly double attempt %d (%v)", i, d, i-1, prev)
		}
		prev = d
	}
}

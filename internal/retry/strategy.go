package retry

import (
	"math"
	"math/rand"
	"time"
)

type Strategy struct {
	baseWait time.Duration
	maxWait  time.Duration
}

func NewStrategy(baseWait, maxWait time.Duration) *Strategy {
	return &Strategy{
		baseWait: baseWait,
		maxWait:  maxWait,
	}
}

// NextDelay calculates exponential backoff with jitter: min(baseWait * 2^attempt, maxWait) + jitter
func (s *Strategy) NextDelay(attempt int) time.Duration {
	delay := float64(s.baseWait) * math.Pow(2, float64(attempt-1))
	if delay > float64(s.maxWait) {
		delay = float64(s.maxWait)
	}

	// Add jitter: ±25%
	jitter := delay * 0.25 * (rand.Float64()*2 - 1)
	delay += jitter

	return time.Duration(delay)
}

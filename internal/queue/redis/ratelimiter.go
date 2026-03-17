package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/insider/insider/internal/domain"
)

// Sliding window rate limiter using a Lua script for atomicity
var rateLimitScript = redis.NewScript(`
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

-- Remove expired entries
redis.call('ZREMRANGEBYSCORE', key, 0, now - window)

-- Count current entries
local count = redis.call('ZCARD', key)

if count < limit then
    redis.call('ZADD', key, now, now .. '-' .. math.random(1000000))
    redis.call('EXPIRE', key, window / 1000000000 + 1)
    return 1
end

return 0
`)

type RateLimiter struct {
	client    *redis.Client
	limit     int
	windowNs  int64
}

func NewRateLimiter(client *redis.Client, limit int) *RateLimiter {
	return &RateLimiter{
		client:   client,
		limit:    limit,
		windowNs: int64(time.Second),
	}
}

func (rl *RateLimiter) Allow(ctx context.Context, channel domain.Channel) (bool, error) {
	key := fmt.Sprintf("ratelimit:%s", channel)
	now := time.Now().UnixNano()

	result, err := rateLimitScript.Run(ctx, rl.client, []string{key}, rl.limit, rl.windowNs, now).Int()
	if err != nil {
		return false, fmt.Errorf("rate limit check: %w", err)
	}

	return result == 1, nil
}

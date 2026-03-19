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

// rateLimitBatchScript allows up to N entries atomically, returning how many were actually added.
var rateLimitBatchScript = redis.NewScript(`
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local requested = tonumber(ARGV[4])

-- Remove expired entries
redis.call('ZREMRANGEBYSCORE', key, 0, now - window)

-- Count current entries
local count = redis.call('ZCARD', key)
local remaining = limit - count
if remaining <= 0 then
    return 0
end

local allowed = math.min(requested, remaining)
for i = 1, allowed do
    redis.call('ZADD', key, now, now .. '-' .. i .. '-' .. math.random(1000000))
end
redis.call('EXPIRE', key, window / 1000000000 + 1)
return allowed
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

// AllowN checks the rate limit for a batch of count items.
// Returns the number of items actually allowed (0 to count).
func (rl *RateLimiter) AllowN(ctx context.Context, channel domain.Channel, count int) (int, error) {
	if count <= 0 {
		return 0, nil
	}

	key := fmt.Sprintf("ratelimit:%s", channel)
	now := time.Now().UnixNano()

	result, err := rateLimitBatchScript.Run(ctx, rl.client, []string{key}, rl.limit, rl.windowNs, now, count).Int()
	if err != nil {
		return 0, fmt.Errorf("rate limit batch check: %w", err)
	}

	return result, nil
}

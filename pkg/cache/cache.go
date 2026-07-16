package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type FullCache interface {
	Cacher
	RedisProvider
}

type RedisProvider interface {
	Redis() *redis.Client
}

// Cacher is the interface for all cache operations.
// Use this in application code so tests can inject a MockCache.
type Cacher interface {
	// Set stores v (JSON-serialized) under key with the given TTL.
	// Pass 0 for ttl to store without expiry.
	Set(ctx context.Context, key string, v any, ttl time.Duration) error

	// Get deserializes the cached value into v.
	// Returns ErrCacheMiss if the key does not exist.
	Get(ctx context.Context, key string, v any) error

	// Delete removes one or more keys.
	Delete(ctx context.Context, keys ...string) error

	// Exists reports whether the key is present.
	Exists(ctx context.Context, key string) (bool, error)

	// SetNX stores key=value only if key does not exist (atomic).
	// Returns true if the key was set, false if it already existed.
	SetNX(ctx context.Context, key string, v any, ttl time.Duration) (bool, error)

	// IncrWithExpiry atomically increments a counter and sets TTL on creation.
	// This is the standard sliding-window rate-limit primitive.
	IncrWithExpiry(ctx context.Context, key string, ttl time.Duration) (int64, error)

	// GetOrSet returns the cached value; if absent it calls fetch, caches and returns the result.
	GetOrSet(ctx context.Context, key string, ttl time.Duration, dest any, fetch func() (any, error)) error

	// HealthCheck verifies the cache is reachable.
	HealthCheck(ctx context.Context) error

	// Close closes the underlying connection pool.
	Close() error
}

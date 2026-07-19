package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrCacheMiss is returned by Get when the key does not exist.
var ErrCacheMiss = errors.New("cache: key not found")

// Cache is the production implementation of the Cacher interface.
type Cache struct {
	client *redis.Client
}

// Compile-time interface compliance check.
var _ Cacher = (*Cache)(nil)

// New creates a Redis client and verifies connectivity before returning.
func New(ctx context.Context, cfg Config) (*Cache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:            cfg.Addr,
		Password:        cfg.Password,
		DB:              cfg.DB,
		MaxRetries:      cfg.MaxRetries,
		DialTimeout:     cfg.DialTimeout,
		ReadTimeout:     cfg.ReadTimeout,
		WriteTimeout:    cfg.WriteTimeout,
		PoolSize:        cfg.PoolSize,
		MinIdleConns:    cfg.MinIdleConns,
		PoolTimeout:     cfg.PoolTimeout,
		MinRetryBackoff: 8 * time.Millisecond,
		MaxRetryBackoff: 512 * time.Millisecond,
	})

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: ping: %w", err)
	}

	slog.Info("redis: connected", "addr", cfg.Addr, "db", cfg.DB)
	return &Cache{client: client}, nil
}

// NewFromClient wraps an existing *redis.Client — useful for testing with miniredis.
func NewFromClient(client *redis.Client) *Cache {
	return &Cache{client: client}
}

// NewClientWithoutPing builds a *redis.Client from cfg WITHOUT verifying
// connectivity first (docs/plan/45 Task T3/K4) — for a caller that must be
// able to START even if Redis isn't reachable YET (fraud-service's
// FailClosedVelocityStore keeps probing in the background and starts
// serving amount-threshold screening immediately regardless). go-redis
// clients are lazy by construction; the only thing New's own eager Ping
// adds is "fail fast at startup instead of on first use" — exactly the
// behavior this constructor deliberately skips. Every other Redis consumer
// in this codebase should keep using New, not this.
func NewClientWithoutPing(cfg Config) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:            cfg.Addr,
		Password:        cfg.Password,
		DB:              cfg.DB,
		MaxRetries:      cfg.MaxRetries,
		DialTimeout:     cfg.DialTimeout,
		ReadTimeout:     cfg.ReadTimeout,
		WriteTimeout:    cfg.WriteTimeout,
		PoolSize:        cfg.PoolSize,
		MinIdleConns:    cfg.MinIdleConns,
		PoolTimeout:     cfg.PoolTimeout,
		MinRetryBackoff: 8 * time.Millisecond,
		MaxRetryBackoff: 512 * time.Millisecond,
	})
}

// Client exposes the underlying client for advanced operations.
func (c *Cache) Client() *redis.Client {
	return c.client
}

func (c *Cache) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if err := c.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis: ping: %w", err)
	}
	return nil
}

func (c *Cache) Close() error {
	slog.Info("redis: closing pool")
	return c.client.Close()
}

func (c *Cache) Set(ctx context.Context, key string, v any, ttl time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("cache set: marshal: %w", err)
	}
	if err := c.client.Set(ctx, key, b, ttl).Err(); err != nil {
		return fmt.Errorf("cache set: %w", err)
	}
	return nil
}

func (c *Cache) Get(ctx context.Context, key string, v any) error {
	b, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrCacheMiss
	}
	if err != nil {
		return fmt.Errorf("cache get: %w", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("cache get: unmarshal: %w", err)
	}
	return nil
}

func (c *Cache) Delete(ctx context.Context, keys ...string) error {
	if err := c.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("cache delete: %w", err)
	}
	return nil
}

func (c *Cache) Exists(ctx context.Context, key string) (bool, error) {
	n, err := c.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("cache exists: %w", err)
	}
	return n > 0, nil
}

func (c *Cache) SetNX(ctx context.Context, key string, v any, ttl time.Duration) (bool, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return false, fmt.Errorf("cache setnx: marshal: %w", err)
	}
	ok, err := c.client.SetNX(ctx, key, b, ttl).Result() //nolint:staticcheck // go-redis keeps this atomic helper for compatibility.
	if err != nil {
		return false, fmt.Errorf("cache setnx: %w", err)
	}
	return ok, nil
}

// IncrWithExpiry atomically increments key and sets TTL if this is the first increment.
func (c *Cache) IncrWithExpiry(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	pipe := c.client.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, ttl)

	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("cache incr with expiry: %w", err)
	}
	return incr.Val(), nil
}

// GetOrSet implements the cache-aside pattern. On a miss, fetch is called and
// the result is cached. A Set failure is logged but does not fail the caller.
func (c *Cache) GetOrSet(ctx context.Context, key string, ttl time.Duration, dest any, fetch func() (any, error)) error {
	err := c.Get(ctx, key, dest)
	if err == nil {
		return nil // cache hit
	}
	if !errors.Is(err, ErrCacheMiss) {
		return err
	}

	// Cache miss — fetch fresh value
	val, err := fetch()
	if err != nil {
		return fmt.Errorf("cache get-or-set fetch: %w", err)
	}

	// Marshal into dest
	b, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("cache get-or-set marshal: %w", err)
	}
	if err := json.Unmarshal(b, dest); err != nil {
		return fmt.Errorf("cache get-or-set unmarshal: %w", err)
	}

	// Best-effort store
	if setErr := c.Set(ctx, key, val, ttl); setErr != nil {
		slog.Warn("cache get-or-set: store failed", "key", key, "error", setErr)
	}

	return nil
}

func (c *Cache) Redis() *redis.Client {
	return c.client
}

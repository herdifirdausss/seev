package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// MockCache is a configurable test double for the Cacher interface.
//
// Usage:
//
//	c := &cache.MockCache{
//	    GetFn: func(ctx context.Context, key string, v any) error {
//	        return cache.ErrCacheMiss
//	    },
//	}
type MockCache struct {
	SetFn            func(ctx context.Context, key string, v any, ttl time.Duration) error
	GetFn            func(ctx context.Context, key string, v any) error
	DeleteFn         func(ctx context.Context, keys ...string) error
	ExistsFn         func(ctx context.Context, key string) (bool, error)
	SetNXFn          func(ctx context.Context, key string, v any, ttl time.Duration) (bool, error)
	IncrWithExpiryFn func(ctx context.Context, key string, ttl time.Duration) (int64, error)
	GetOrSetFn       func(ctx context.Context, key string, ttl time.Duration, dest any, fetch func() (any, error)) error
	HealthCheckFn    func(ctx context.Context) error
	CloseFn          func() error
	RedisFn          func() *redis.Client
}

// Compile-time interface check.
var _ Cacher = (*MockCache)(nil)
var _ RedisProvider = (*MockCache)(nil)

func (m *MockCache) Set(ctx context.Context, key string, v any, ttl time.Duration) error {
	if m.SetFn != nil {
		return m.SetFn(ctx, key, v, ttl)
	}
	return nil
}

func (m *MockCache) Get(ctx context.Context, key string, v any) error {
	if m.GetFn != nil {
		return m.GetFn(ctx, key, v)
	}
	return nil
}

func (m *MockCache) Delete(ctx context.Context, keys ...string) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, keys...)
	}
	return nil
}

func (m *MockCache) Exists(ctx context.Context, key string) (bool, error) {
	if m.ExistsFn != nil {
		return m.ExistsFn(ctx, key)
	}
	return false, nil
}

func (m *MockCache) SetNX(ctx context.Context, key string, v any, ttl time.Duration) (bool, error) {
	if m.SetNXFn != nil {
		return m.SetNXFn(ctx, key, v, ttl)
	}
	return true, nil
}

func (m *MockCache) IncrWithExpiry(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	if m.IncrWithExpiryFn != nil {
		return m.IncrWithExpiryFn(ctx, key, ttl)
	}
	return 1, nil
}

func (m *MockCache) GetOrSet(ctx context.Context, key string, ttl time.Duration, dest any, fetch func() (any, error)) error {
	if m.GetOrSetFn != nil {
		return m.GetOrSetFn(ctx, key, ttl, dest, fetch)
	}
	return nil
}

func (m *MockCache) HealthCheck(ctx context.Context) error {
	if m.HealthCheckFn != nil {
		return m.HealthCheckFn(ctx)
	}
	return nil
}

func (m *MockCache) Close() error {
	if m.CloseFn != nil {
		return m.CloseFn()
	}
	return nil
}

func (m *MockCache) Redis() *redis.Client {
	if m.RedisFn != nil {
		return m.RedisFn()
	}
	return nil
}

type MockLimiter struct {
	AllowFn func(ctx context.Context, key string) (bool, int64, error)
}

func (m *MockLimiter) Allow(ctx context.Context, key string) (bool, int64, error) {
	if m.AllowFn != nil {
		return m.AllowFn(ctx, key)
	}
	return true, 0, nil
}

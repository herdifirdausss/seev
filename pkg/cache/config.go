package cache

import "time"

// Config configures a Redis client.
type Config struct {
	Enabled      bool
	Addr         string
	Password     string
	Username     string
	DB           int
	MaxRetries   int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	PoolSize     int
	MinIdleConns int
	PoolTimeout  time.Duration
}

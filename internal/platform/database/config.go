package database

import (
	"fmt"
	"strings"
	"time"
)

// Config configures a PostgreSQL connection pool.
type Config struct {
	Host             string
	Port             string
	User             string
	Password         string
	DB               string
	SSLMode          string
	MaxOpenConns     int
	MaxIdleConns     int
	ConnMaxLifetime  time.Duration
	ConnMaxIdleTime  time.Duration
	StatementTimeout time.Duration
	LockTimeout      time.Duration
	IdleInTxTimeout  time.Duration
}

// DSN returns a libpq-style PostgreSQL connection string.
func (c Config) DSN() string {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.DB, c.SSLMode,
	)
	if opts := c.sessionOptions(); opts != "" {
		dsn += fmt.Sprintf(" options='%s'", opts)
	}
	return dsn
}

func (c Config) sessionOptions() string {
	var parts []string
	if c.StatementTimeout > 0 {
		parts = append(parts, fmt.Sprintf("-c statement_timeout=%d", c.StatementTimeout.Milliseconds()))
	}
	if c.LockTimeout > 0 {
		parts = append(parts, fmt.Sprintf("-c lock_timeout=%d", c.LockTimeout.Milliseconds()))
	}
	if c.IdleInTxTimeout > 0 {
		parts = append(parts, fmt.Sprintf("-c idle_in_transaction_session_timeout=%d", c.IdleInTxTimeout.Milliseconds()))
	}
	return strings.Join(parts, " ")
}

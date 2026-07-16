package messaging

// config.go — connection-level broker configuration.
//
// Intentionally decoupled from queue topology. Queues, exchanges, and bindings
// are declared at runtime via DeclareTopology, enabling multiple modules to
// share a single broker connection with independent queue configurations.
//
// Migration from config.RabbitMQConfig:
//
//	old := config.RabbitMQConfig{ ... }
//	cfg := messagingv2.BrokerConfig{
//	    Host:            old.Host,
//	    Username:        old.Username,
//	    Password:        old.Password,
//	    DefaultExchange: old.Exchange,
//	    // ... etc.
//	}

import (
	"crypto/tls"
	"fmt"
	"time"
)

const (
	defaultAMQPPort  = 5672
	defaultAMQPSPort = 5671
	defaultVHost     = "/"
)

// BrokerConfig holds connection-level configuration for the RabbitMQ broker.
// All queue/routing-key specifics are intentionally absent — they belong in
// QueueConfig and ConsumeOptions, not here.
type BrokerConfig struct {
	// ── Connection ──────────────────────────────────────────────────────────
	Host     string      // required
	Port     int         // default: 5672 (AMQP) or 5671 (AMQPS)
	VHost    string      // default: "/"
	Username string      // required
	Password string      // required
	TLS      *tls.Config // nil → plain AMQP; non-nil → AMQPS

	// ── Identity ─────────────────────────────────────────────────────────────
	// AppID is stamped on every published message for origin tracing.
	AppID string

	// DefaultExchange is used by Publish() and PublishWithID().
	// Individual publishes can override via PublishTo().
	DefaultExchange string // required

	// ── Timeouts & Resilience ─────────────────────────────────────────────────
	DialTimeout          time.Duration // TCP dial timeout; default: 10s
	PublishTimeout       time.Duration // confirm-wait deadline per message; default: 5s
	ReconnectBaseDelay   time.Duration // base for exponential backoff; default: 1s
	MaxReconnectAttempts int           // 0 = unlimited; default: 10

	// ── Resource Limits ───────────────────────────────────────────────────────
	ChannelPoolSize      int           // max idle confirm channels; default: 16
	MaxConcurrentPublish int           // publish semaphore width; default: 64
	DrainTimeout         time.Duration // graceful-close drain budget; default: 30s
}

// Validate returns an error if any required fields are missing or invalid.
func (c *BrokerConfig) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("BrokerConfig: Host is required")
	}
	if c.Username == "" {
		return fmt.Errorf("BrokerConfig: Username is required")
	}
	if c.Password == "" {
		return fmt.Errorf("BrokerConfig: Password is required")
	}
	if c.DefaultExchange == "" {
		return fmt.Errorf("BrokerConfig: DefaultExchange is required")
	}
	return nil
}

// url returns the full AMQP(S) connection URL including credentials.
func (c *BrokerConfig) url() string {
	scheme := "amqp"
	if c.TLS != nil {
		scheme = "amqps"
	}
	return fmt.Sprintf("%s://%s:%s@%s:%d%s",
		scheme, c.Username, c.Password, c.Host, c.port(), c.vhost())
}

// safeAddr returns host:port/vhost without credentials — safe for logs.
func (c *BrokerConfig) safeAddr() string {
	return fmt.Sprintf("%s:%d%s", c.Host, c.port(), c.vhost())
}

func (c *BrokerConfig) port() int {
	if c.Port > 0 {
		return c.Port
	}
	if c.TLS != nil {
		return defaultAMQPSPort
	}
	return defaultAMQPPort
}

func (c *BrokerConfig) vhost() string {
	if c.VHost == "" {
		return defaultVHost
	}
	return c.VHost
}

// withDefaults returns a copy of c with zero-value fields filled in.
func (c BrokerConfig) withDefaults() BrokerConfig {
	if c.DialTimeout <= 0 {
		c.DialTimeout = defaultDialTimeout
	}
	if c.PublishTimeout <= 0 {
		c.PublishTimeout = defaultPublishTimeout
	}
	if c.ReconnectBaseDelay <= 0 {
		c.ReconnectBaseDelay = time.Second
	}
	if c.MaxReconnectAttempts == 0 {
		c.MaxReconnectAttempts = 10
	}
	if c.ChannelPoolSize <= 0 {
		c.ChannelPoolSize = defaultChannelPoolSize
	}
	if c.MaxConcurrentPublish <= 0 {
		c.MaxConcurrentPublish = defaultMaxConcurrentPublish
	}
	if c.DrainTimeout <= 0 {
		c.DrainTimeout = defaultDrainTimeout
	}
	return c
}

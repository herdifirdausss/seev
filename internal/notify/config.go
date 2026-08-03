package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"net/http"
	"time"

	"github.com/herdifirdausss/seev/internal/notify/channel"
	"github.com/herdifirdausss/seev/pkg/cryptox"
)

// Config keeps notification delivery feature-gated. In-app is enabled by
// default; external providers are opt-in and can be paused independently.
type Config struct {
	Enabled       bool
	InAppEnabled  bool
	EmailEnabled  bool
	PushEnabled   bool
	DigestEnabled bool

	DefaultLocale    string
	DefaultTimezone  string
	DigestHour       int
	EventPrefetch    int
	MaxEventAttempts int
	DeliveryBatch    int
	DeliveryLease    time.Duration
	ProviderTimeout  time.Duration
	EmailWorkers     int
	PushWorkers      int
	ContactWorkers   int
	DigestWorkers    int
	MaxDevices       int

	EncryptionRing *cryptox.Ring
	FingerprintKey []byte
	AuthContactURL string
	InternalToken  string
	HTTPClient     *http.Client
	EmailSender    channel.EmailSender
	PushSender     channel.PushSender
}

func DefaultConfig() Config {
	return Config{
		Enabled: true, InAppEnabled: true, DefaultLocale: "en-US", DefaultTimezone: "Asia/Jakarta",
		DigestHour: 8, EventPrefetch: 10, MaxEventAttempts: 5, DeliveryBatch: 50,
		DeliveryLease: 2 * time.Minute, ProviderTimeout: 10 * time.Second,
		EmailWorkers: 2, PushWorkers: 2, ContactWorkers: 2, DigestWorkers: 1, MaxDevices: 10,
	}
}

func (c Config) fingerprint(value string) []byte {
	key := c.FingerprintKey
	if len(key) == 0 {
		key = []byte("seev-notification-fingerprint-development-key")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

type contact struct {
	UserID        string    `json:"user_id"`
	Email         string    `json:"email"`
	EmailVerified bool      `json:"email_verified"`
	UserStatus    string    `json:"user_status"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type ContactResolver interface {
	Resolve(ctx context.Context, userID string) (email string, verified bool, active bool, err error)
}

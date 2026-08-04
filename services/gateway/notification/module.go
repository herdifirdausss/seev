// Package notify is the stable public facade for Gateway Notifications.
// Business decisions live in inbox/; persistence, channels, templates, and
// HTTP concerns stay in dedicated packages.
package notify

import (
	"log/slog"
	"time"

	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/services/gateway/internal/notification/channel"
	inbox "github.com/herdifirdausss/seev/services/gateway/internal/notification/inbox"
)

var ErrNotificationNotFound = inbox.ErrNotificationNotFound

type (
	Broker              = inbox.Broker
	Config              = inbox.Config
	ContactResolver     = inbox.ContactResolver
	HTTPContactResolver = inbox.HTTPContactResolver
	Module              = inbox.Module
	Notification        = inbox.Notification
)

func NewModule(db database.DatabaseSQL, broker Broker, logger *slog.Logger) *Module {
	return inbox.NewModule(db, broker, logger)
}

func NewConfiguredModule(db database.DatabaseSQL, broker Broker, cfg Config, logger *slog.Logger, contactResolver ContactResolver, emailSender channel.EmailSender, pushSender channel.PushSender) *Module {
	return inbox.NewConfiguredModule(db, broker, cfg, logger, contactResolver, emailSender, pushSender)
}

func DefaultConfig() Config { return inbox.DefaultConfig() }

func NextAllowedTime(now time.Time, timezone string, enabled bool, start, end *string) (time.Time, error) {
	return inbox.NextAllowedTime(now, timezone, enabled, start, end)
}

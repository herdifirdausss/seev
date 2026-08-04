// Package adminbff is the stable public facade for the Admin BFF.
// Sessions, proxy orchestration, and audit behavior live in internal/admin;
// downstream clients and web assets remain in their own packages.
package adminbff

import (
	"context"
	"log/slog"

	"github.com/herdifirdausss/seev/internal/platform/config"
	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/internal/platform/security/tls"
	admininternal "github.com/herdifirdausss/seev/services/adminbff/internal/admin"
)

var (
	ErrInvalidOperator = admininternal.ErrInvalidOperator
	ErrSessionNotFound = admininternal.ErrSessionNotFound
)

type (
	AuditEntry        = admininternal.AuditEntry
	AuditFilter       = admininternal.AuditFilter
	AuthClient        = admininternal.AuthClient
	AuthUser          = admininternal.AuthUser
	Module            = admininternal.Module
	Session           = admininternal.Session
	SessionRepository = admininternal.SessionRepository
)

func NewModule(db database.DatabaseSQL, cfg config.AdminBFFConfig, logger *slog.Logger, certSrc *tlsx.CertSource, ring *cryptox.Ring) *Module {
	return admininternal.NewModule(db, cfg, logger, certSrc, ring)
}

func NewAuthClient(baseURL string) *AuthClient { return admininternal.NewAuthClient(baseURL) }
func NewOpaqueToken(size int) (string, error)  { return admininternal.NewOpaqueToken(size) }
func NewSessionRepository(db database.DatabaseSQL, ring *cryptox.Ring) SessionRepository {
	return admininternal.NewSessionRepository(db, ring)
}
func SessionFromContext(ctx context.Context) *Session { return admininternal.SessionFromContext(ctx) }

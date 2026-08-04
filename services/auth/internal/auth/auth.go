// Package auth is the public facade for the auth module (docs/roadmap/archive/25 Task
// T1, shape locked by docs/roadmap/archive/24's services/auth outline and decision D12)
// — identity, credentials, and token issuance for end users. This is the
// ONLY package other code may import from services/auth — importing
// services/auth/internal/repository or services/auth/internal/auth/model directly from outside
// this module is a boundary violation (docs/roadmap/archive/01-target-architecture.md,
// enforced by boundary_test.go).
//
// JWTs issued here use the EXACT claims contract internal/platform/security/middleware already
// verifies (UserID/Email/Role/Exp/Iss) — nothing in ledger/policy/middleware
// changes because this module exists; they keep trusting the same tokens.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"golang.org/x/crypto/bcrypt"

	"github.com/herdifirdausss/seev/contracts/clients/fraud"
	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/internal/platform/security/middleware"
	"github.com/herdifirdausss/seev/services/auth/internal/adapter/kycvendor"
	"github.com/herdifirdausss/seev/services/auth/internal/auth/model"
	"github.com/herdifirdausss/seev/services/auth/internal/repository"
)

// Re-exported types so callers never need to import services/auth/internal/auth/model.
type User = model.User

// bcryptCost 12 ≈ 250ms per hash on commodity hardware — the standard
// fintech cost/latency tradeoff (10 is too cheap against offline cracking,
// 14 makes login noticeably slow).
const bcryptCost = 12

// Provisioner is the subset of ledger.Module's behavior auth needs — a
// local structural interface (mirrors payin.Poster / payout.Poster) rather
// than a dependency on the concrete *ledger.Module type. Referencing
// ledger.Account (the ROOT facade's re-export) is the established pattern —
// payin.Poster does the same with ledgerclient.Command. ProvisionUser is
// idempotent on the ledger side (upsert), so calling it again for an
// already-provisioned user is always safe.
type Provisioner interface {
	ProvisionUser(ctx context.Context, userID uuid.UUID, currency string) error
	// ApplyKycTier upserts a user's effective policy_limits from the ledger's
	// policy_tier_limits template for kycLevel (docs/roadmap/archive/39 Task T5) —
	// called synchronously inside ApproveKYCSubmission's transaction so a
	// failure here rolls back the whole approval (gotcha #10 master:
	// kyc_level must never advance ahead of its enforced limits).
	ApplyKycTier(ctx context.Context, userID uuid.UUID, kycLevel int) error
}

// ExecutionSubjectProvisioner is an additive integration seam. Ledger keeps a
// fail-closed projection of auth status/KYC so queued commands are rechecked at
// execution time. Older test doubles and deployments may omit the method; the
// base account/policy provisioning contract remains source-compatible.
type ExecutionSubjectProvisioner interface {
	SetExecutionSubjectState(context.Context, uuid.UUID, string, int, *time.Time) error
}

// Config carries the knobs auth needs from the composition root.
type Config struct {
	JWTSecret       string
	JWTIssuer       string
	AccessExpiry    time.Duration // e.g. 15m
	RefreshExpiry   time.Duration // e.g. 168h
	DefaultCurrency string        // currency ProvisionUser uses for new users, e.g. "IDR"
	// KYCValidityTTL is how long an approved KYC level stays valid before the
	// periodic expiry worker (services/auth/internal/worker/expiry.go) downgrades it
	// back to L0, forcing re-verification. Zero defaults to 365 days
	// (approveSubmission) — every existing Config{} literal in this repo
	// predates this field and keeps working unchanged.
	KYCValidityTTL time.Duration
}

// Module is the public facade for the auth module.
type Module struct {
	db            database.DatabaseSQL
	users         repository.UserRepository
	refreshTokens repository.RefreshTokenRepository
	kyc           repository.KYCRepository
	provisioner   Provisioner
	cfg           Config
	logger        *slog.Logger
	kycProvider   kycvendor.Provider
	// cryptoxRing/cryptoxLookup are the SAME ring/lookup key NewModule
	// already used to construct users/kyc above — retained here for the
	// small number of direct-SQL call sites in this package that
	// deliberately bypass UserRepository/KYCRepository
	// (closure_worker.go's tombstone, privacy_worker.go's export row
	// collector) and therefore need to seal/open auth_users.email/
	// full_name (and compute the lookup digest) themselves, the exact same
	// way user_repository.go does.
	cryptoxRing      *cryptox.Ring
	cryptoxLookup    *cryptox.LookupKey
	sanctionsChecker interface {
		CheckWithSubject(context.Context, string, string, uuid.UUID, decimal.Decimal, string, string, string) (fraudcheck.Verdict, error)
	}
	documentStore DocumentStore
	documentRing  *cryptox.Ring
	exportRing    *cryptox.Ring
	// closureRing/closureOwners back docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T5's
	// (K10) account-closure saga — both optional/nil-safe like exportRing
	// above (RequestClosure returns ErrClosureUnavailable until the ring
	// and at least one owner are wired), never required at NewModule
	// construction. closureOwners is an ORDERED registry (A8 T5b: ledger
	// alone at T5, payin/payout/fraud/gateway added at T5b) — order is
	// the saga's own owner-processing order, fixed at registration time
	// so a resumed saga's "next unprepared/uncommitted owner" lookup is
	// deterministic across restarts.
	closureRing   *cryptox.Ring
	closureOwners []closureOwnerRegistration
}

// SetSanctionsChecker enables the optional fraud-service sanctions seam. A
// nil checker preserves the existing KYC-only behavior for local development.
func (m *Module) SetSanctionsChecker(checker interface {
	CheckWithSubject(context.Context, string, string, uuid.UUID, decimal.Decimal, string, string, string) (fraudcheck.Verdict, error)
}) {
	m.sanctionsChecker = checker
}

// NewModule wires the auth module. ring/lookup are REQUIRED —
// docs/roadmap/archive/51-a8-data-lifecycle-privacy.md "A8 T2.5b" (the
// contract migration) removed the plaintext fallback for
// auth_users.email/full_name and kyc_submissions.payload, so there is no
// longer a valid "cryptox unconfigured" mode to construct: the caller
// (services/auth/cmd/auth/main.go) builds the ring/lookup unconditionally at
// boot and fails the process if it can't, the same "money-safety, never
// optional" posture T3's LedgerIdempotency ring already established.
// NewUserRepository/NewKYCRepository themselves panic on a nil ring as
// the last-resort backstop if this is ever miswired.
func NewModule(db database.DatabaseSQL, provisioner Provisioner, cfg Config, logger *slog.Logger, ring *cryptox.Ring, lookup *cryptox.LookupKey, providers ...kycvendor.Provider) *Module {
	if logger == nil {
		logger = slog.Default()
	}
	provider := kycvendor.Provider(unavailableKYCProvider{})
	if len(providers) > 0 && providers[0] != nil {
		provider = providers[0]
	}
	return &Module{
		db:            db,
		users:         repository.NewUserRepository(db, ring, lookup),
		refreshTokens: repository.NewRefreshTokenRepository(db),
		kyc:           repository.NewKYCRepository(db, ring),
		provisioner:   provisioner,
		cfg:           cfg,
		logger:        logger,
		kycProvider:   provider,
		cryptoxRing:   ring,
		cryptoxLookup: lookup,
	}
}

// TokenPair is what Login/Refresh/Register hand back to the transport layer.
type TokenPair struct {
	AccessToken      string
	RefreshToken     string // the OPAQUE token — shown to the client exactly once
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

// Register creates a new identity, provisions its ledger accounts, and logs
// it straight in (returning a token pair) — one round trip from "no account"
// to "usable wallet".
func (m *Module) Register(ctx context.Context, email, password, fullName string) (User, TokenPair, error) {
	if err := validateEmail(email); err != nil {
		return User{}, TokenPair{}, err
	}
	if err := validatePassword(password); err != nil {
		return User{}, TokenPair{}, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return User{}, TokenPair{}, fmt.Errorf("auth: hash password: %w", err)
	}

	u := model.User{
		ID:       uuid.New(),
		Email:    email,
		FullName: fullName,
		Role:     model.RoleUser,
		Status:   model.StatusActive,
	}
	if err := m.users.CreateUser(ctx, u, string(hash)); err != nil {
		if errors.Is(err, repository.ErrDuplicateEmail) {
			return User{}, TokenPair{}, ErrEmailTaken
		}
		return User{}, TokenPair{}, err
	}

	// Provision the ledger account set. A failure here is NOT fatal to
	// registration — the identity row is committed, and Login lazily
	// re-provisions (ProvisionUser is idempotent), so the user self-heals
	// on their first successful login instead of being stuck half-created.
	if err := m.provision(ctx, u.ID); err != nil {
		m.logger.Error("auth: provision on register failed, will retry on login",
			slog.Any("error", err), slog.String("user_id", u.ID.String()))
	}

	pair, err := m.issueTokens(ctx, u)
	if err != nil {
		return User{}, TokenPair{}, err
	}
	return u, pair, nil
}

// Login verifies credentials and issues a fresh token pair. Also lazily
// re-provisions ledger accounts (idempotent) so a register whose provision
// step failed heals here.
func (m *Module) Login(ctx context.Context, email, password string) (User, TokenPair, error) {
	u, err := m.users.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Burn roughly the same time as a real bcrypt compare so the
			// timing side channel doesn't reveal account existence either.
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
			return User{}, TokenPair{}, ErrInvalidCredentials
		}
		return User{}, TokenPair{}, err
	}

	hash, err := m.users.GetPasswordHash(ctx, u.ID)
	if err != nil {
		return User{}, TokenPair{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return User{}, TokenPair{}, ErrInvalidCredentials
	}
	if u.Status != model.StatusActive {
		return User{}, TokenPair{}, ErrUserDisabled
	}

	if err := m.provision(ctx, u.ID); err != nil {
		m.logger.Error("auth: lazy provision on login failed",
			slog.Any("error", err), slog.String("user_id", u.ID.String()))
	}
	// provision() creates the baseline L0 projection for a newly registered
	// identity. A returning user may already have an approved KYC tier, so
	// restore the authoritative auth state after the idempotent account
	// provisioning call; otherwise a login could silently downgrade the ledger
	// projection while the JWT still carries the higher tier.
	if err := m.syncExecutionSubject(ctx, u.ID, u.Status, u.KYCLevel, u.KYCVerifiedUntil); err != nil {
		m.logger.Error("auth: synchronize execution subject on login failed",
			slog.Any("error", err), slog.String("user_id", u.ID.String()))
	}

	pair, err := m.issueTokens(ctx, u)
	if err != nil {
		return User{}, TokenPair{}, err
	}
	return u, pair, nil
}

// Refresh rotates a refresh token: the presented token is revoked and a new
// pair is issued. Presenting a token that was ALREADY revoked is treated as
// replay — every live token the user has is revoked and the caller gets 401
// (docs/roadmap/archive/25 T1 step 2).
func (m *Module) Refresh(ctx context.Context, refreshToken string) (User, TokenPair, error) {
	t, err := m.refreshTokens.GetRefreshTokenByHash(ctx, hashToken(refreshToken))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return User{}, TokenPair{}, ErrInvalidRefreshToken
		}
		return User{}, TokenPair{}, err
	}

	if t.RevokedAt != nil {
		// Replay: this token was already used once. Someone is holding a
		// stale copy — kill the whole chain.
		m.logger.Warn("auth: revoked refresh token presented, revoking all user tokens",
			slog.String("user_id", t.UserID.String()))
		if err := m.refreshTokens.RevokeAllForUser(ctx, t.UserID); err != nil {
			m.logger.Error("auth: revoke-all after replay failed", slog.Any("error", err))
		}
		return User{}, TokenPair{}, ErrInvalidRefreshToken
	}
	if time.Now().After(t.ExpiresAt) {
		return User{}, TokenPair{}, ErrInvalidRefreshToken
	}

	u, err := m.users.GetUserByID(ctx, t.UserID)
	if err != nil {
		return User{}, TokenPair{}, err
	}
	if u.Status != model.StatusActive {
		return User{}, TokenPair{}, ErrUserDisabled
	}

	// Issue the successor FIRST, then revoke the old one pointing at it —
	// a crash in between leaves two live tokens (harmless: both are the
	// same user, the old one still rotates-or-revokes on next use), never
	// zero (which would log the user out spuriously).
	pair, newTokenID, err := m.issueTokensWithID(ctx, u)
	if err != nil {
		return User{}, TokenPair{}, err
	}
	won, err := m.refreshTokens.RevokeRefreshToken(ctx, t.ID, &newTokenID)
	if err != nil {
		return User{}, TokenPair{}, err
	}
	if !won {
		// A concurrent refresh raced us and revoked it first — treat like
		// replay-adjacent: our freshly issued pair stands, but log it.
		m.logger.Warn("auth: concurrent refresh detected", slog.String("user_id", u.ID.String()))
	}
	return u, pair, nil
}

// Me returns the profile for an authenticated user id (from JWT claims).
func (m *Module) Me(ctx context.Context, userID uuid.UUID) (User, error) {
	u, err := m.users.GetUserByID(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, err
	}
	// docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T5 (K10): a still-valid (unexpired)
	// access token issued before closure would otherwise keep working here
	// — Login/Refresh already reject on status, but a live access token
	// never round-trips through either of those, so this route needs its
	// own live-status check.
	if u.Status == model.StatusClosing || u.Status == model.StatusClosed {
		return User{}, ErrUserDisabled
	}
	return u, nil
}

// UpdateMe updates the caller's own mutable profile fields (full name only
// for now — email/role/status changes are admin/security flows, not here).
func (m *Module) UpdateMe(ctx context.Context, userID uuid.UUID, fullName string) (User, error) {
	u, err := m.users.GetUserByID(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, err
	}
	if u.Status == model.StatusClosing || u.Status == model.StatusClosed {
		return User{}, ErrUserDisabled
	}
	if err := m.users.UpdateFullName(ctx, userID, fullName); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return User{}, ErrInvalidCredentials
		}
		return User{}, err
	}
	return m.users.GetUserByID(ctx, userID)
}

// ─── internals ───────────────────────────────────────────────────────────────

func (m *Module) provision(ctx context.Context, userID uuid.UUID) error {
	if err := m.provisioner.ProvisionUser(ctx, userID, m.cfg.DefaultCurrency); err != nil {
		return err
	}
	if syncer, ok := m.provisioner.(ExecutionSubjectProvisioner); ok {
		if err := syncer.SetExecutionSubjectState(ctx, userID, model.StatusActive, 0, nil); err != nil {
			return fmt.Errorf("auth: synchronize ledger execution subject: %w", err)
		}
	}
	return nil
}

func (m *Module) syncExecutionSubject(ctx context.Context, userID uuid.UUID, status string, level int, verifiedUntil *time.Time) error {
	syncer, ok := m.provisioner.(ExecutionSubjectProvisioner)
	if !ok {
		return nil
	}
	return syncer.SetExecutionSubjectState(ctx, userID, status, level, verifiedUntil)
}

func (m *Module) issueTokens(ctx context.Context, u model.User) (TokenPair, error) {
	pair, _, err := m.issueTokensWithID(ctx, u)
	return pair, err
}

func (m *Module) issueTokensWithID(ctx context.Context, u model.User) (TokenPair, uuid.UUID, error) {
	now := time.Now()
	accessExp := now.Add(m.cfg.AccessExpiry)
	access, err := middleware.GenerateToken(m.cfg.JWTSecret, middleware.Claims{
		UserID:   u.ID.String(),
		Email:    u.Email,
		Role:     u.Role,
		KYCLevel: u.KYCLevel,
		Exp:      accessExp.Unix(),
		Iss:      m.cfg.JWTIssuer,
	})
	if err != nil {
		return TokenPair{}, uuid.Nil, fmt.Errorf("auth: issue access token: %w", err)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return TokenPair{}, uuid.Nil, fmt.Errorf("auth: generate refresh token: %w", err)
	}
	refresh := base64.RawURLEncoding.EncodeToString(raw)
	refreshExp := now.Add(m.cfg.RefreshExpiry)

	tokenID := uuid.New()
	if err := m.refreshTokens.InsertRefreshToken(ctx, model.RefreshToken{
		ID: tokenID, UserID: u.ID, TokenHash: hashToken(refresh), ExpiresAt: refreshExp,
	}); err != nil {
		return TokenPair{}, uuid.Nil, err
	}

	return TokenPair{
		AccessToken: access, RefreshToken: refresh,
		AccessExpiresAt: accessExp, RefreshExpiresAt: refreshExp,
	}, tokenID, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func validateEmail(email string) error {
	if email == "" {
		return fmt.Errorf("%w: email is required", ErrValidation)
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return fmt.Errorf("%w: invalid email address", ErrValidation)
	}
	return nil
}

func validatePassword(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("%w: password must be at least 8 characters", ErrValidation)
	}
	if len(password) > 72 {
		// bcrypt truncates silently past 72 bytes — reject instead.
		return fmt.Errorf("%w: password must be at most 72 characters", ErrValidation)
	}
	return nil
}

// dummyHash is a valid bcrypt hash of an unguessable value, used to
// equalize login timing when the email doesn't exist.
var dummyHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("timing-equalizer-not-a-real-password"), bcryptCost)
	if err != nil {
		panic(err) // cannot happen with a valid cost
	}
	return h
}()

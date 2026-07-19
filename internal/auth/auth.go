// Package auth is the public facade for the auth module (docs/plan/25 Task
// T1, shape locked by docs/plan/24's internal/auth outline and decision D12)
// — identity, credentials, and token issuance for end users. This is the
// ONLY package other code may import from internal/auth — importing
// internal/auth/repository or internal/auth/model directly from outside
// this module is a boundary violation (docs/plan/01-target-architecture.md,
// enforced by boundary_test.go).
//
// JWTs issued here use the EXACT claims contract pkg/middleware already
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
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/internal/auth/worker"
	"github.com/herdifirdausss/seev/internal/kycvendor"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

// Re-exported types so callers never need to import internal/auth/model.
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
	// policy_tier_limits template for kycLevel (docs/plan/39 Task T5) —
	// called synchronously inside ApproveKYCSubmission's transaction so a
	// failure here rolls back the whole approval (gotcha #10 master:
	// kyc_level must never advance ahead of its enforced limits).
	ApplyKycTier(ctx context.Context, userID uuid.UUID, kycLevel int) error
}

// Config carries the knobs auth needs from the composition root.
type Config struct {
	JWTSecret       string
	JWTIssuer       string
	AccessExpiry    time.Duration // e.g. 15m
	RefreshExpiry   time.Duration // e.g. 168h
	DefaultCurrency string        // currency ProvisionUser uses for new users, e.g. "IDR"
}

// Module is the public facade for the auth module.
type Module struct {
	repo        repository.Repository
	provisioner Provisioner
	cfg         Config
	logger      *slog.Logger
	kycProvider kycvendor.Provider
}

// ErrKYCApplyQueued marks the safe degraded response when the ledger could
// not apply policy limits inline.  The submission remains pending until the
// durable relay completes; callers can use errors.Is without parsing text.
var ErrKYCApplyQueued = errors.New("auth: kyc apply queued for retry")

// KYCApplyQueuedError retains both the durable intent id and the dependency
// error for logs/tests while exposing ErrKYCApplyQueued through errors.Is.
type KYCApplyQueuedError struct {
	RetryID uuid.UUID
	Cause   error
}

func (e *KYCApplyQueuedError) Error() string {
	if e == nil || e.Cause == nil {
		return ErrKYCApplyQueued.Error()
	}
	return fmt.Sprintf("%s: %v", ErrKYCApplyQueued, e.Cause)
}

func (e *KYCApplyQueuedError) Unwrap() error {
	if e == nil {
		return ErrKYCApplyQueued
	}
	return errors.Join(ErrKYCApplyQueued, e.Cause)
}

type unavailableKYCProvider struct{}

func (unavailableKYCProvider) Name() string { return "unconfigured" }
func (unavailableKYCProvider) Verify(context.Context, kycvendor.Submission) (kycvendor.Decision, error) {
	return kycvendor.Decision{}, ErrKYCProvider
}

// NewModule wires the auth module.
func NewModule(db database.DatabaseSQL, provisioner Provisioner, cfg Config, logger *slog.Logger, providers ...kycvendor.Provider) *Module {
	if logger == nil {
		logger = slog.Default()
	}
	provider := kycvendor.Provider(unavailableKYCProvider{})
	if len(providers) > 0 && providers[0] != nil {
		provider = providers[0]
	}
	return &Module{
		repo:        repository.NewRepository(db),
		provisioner: provisioner,
		cfg:         cfg,
		logger:      logger,
		kycProvider: provider,
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
	if err := m.repo.CreateUser(ctx, u, string(hash)); err != nil {
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
	u, err := m.repo.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Burn roughly the same time as a real bcrypt compare so the
			// timing side channel doesn't reveal account existence either.
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
			return User{}, TokenPair{}, ErrInvalidCredentials
		}
		return User{}, TokenPair{}, err
	}

	hash, err := m.repo.GetPasswordHash(ctx, u.ID)
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

	pair, err := m.issueTokens(ctx, u)
	if err != nil {
		return User{}, TokenPair{}, err
	}
	return u, pair, nil
}

// Refresh rotates a refresh token: the presented token is revoked and a new
// pair is issued. Presenting a token that was ALREADY revoked is treated as
// replay — every live token the user has is revoked and the caller gets 401
// (docs/plan/25 T1 step 2).
func (m *Module) Refresh(ctx context.Context, refreshToken string) (User, TokenPair, error) {
	t, err := m.repo.GetRefreshTokenByHash(ctx, hashToken(refreshToken))
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
		if err := m.repo.RevokeAllForUser(ctx, t.UserID); err != nil {
			m.logger.Error("auth: revoke-all after replay failed", slog.Any("error", err))
		}
		return User{}, TokenPair{}, ErrInvalidRefreshToken
	}
	if time.Now().After(t.ExpiresAt) {
		return User{}, TokenPair{}, ErrInvalidRefreshToken
	}

	u, err := m.repo.GetUserByID(ctx, t.UserID)
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
	won, err := m.repo.RevokeRefreshToken(ctx, t.ID, &newTokenID)
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
	u, err := m.repo.GetUserByID(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return User{}, ErrInvalidCredentials
	}
	return u, err
}

// UpdateMe updates the caller's own mutable profile fields (full name only
// for now — email/role/status changes are admin/security flows, not here).
func (m *Module) UpdateMe(ctx context.Context, userID uuid.UUID, fullName string) (User, error) {
	if err := m.repo.UpdateFullName(ctx, userID, fullName); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return User{}, ErrInvalidCredentials
		}
		return User{}, err
	}
	return m.repo.GetUserByID(ctx, userID)
}

// EnsureBootstrapAdmin idempotently creates the first admin account from
// env config (docs/plan/25 T1 step 6) — called once at startup by the
// composition root. Chosen over a seed migration so no password hash is
// ever committed to VCS. No-op when the email already exists.
func (m *Module) EnsureBootstrapAdmin(ctx context.Context, email, password string) error {
	if email == "" || password == "" {
		return nil // bootstrap admin not configured — fine
	}
	if _, err := m.repo.GetUserByEmail(ctx, email); err == nil {
		return nil // already exists
	} else if !errors.Is(err, repository.ErrNotFound) {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return fmt.Errorf("auth: hash bootstrap admin password: %w", err)
	}
	u := model.User{
		ID: uuid.New(), Email: email, FullName: "Bootstrap Admin",
		Role: model.RoleAdmin, Status: model.StatusActive, KYCLevel: 2,
	}
	if err := m.repo.CreateUser(ctx, u, string(hash)); err != nil {
		if errors.Is(err, repository.ErrDuplicateEmail) {
			return nil // raced another replica — fine, it exists
		}
		return err
	}
	m.logger.Info("auth: bootstrap admin created", slog.String("email", email))
	return nil
}

type KYCStatus struct {
	Level      int
	Submission *model.KYCSubmission
}

func (m *Module) SubmitKYC(ctx context.Context, userID uuid.UUID, levelRequested int, payload map[string]any) (model.KYCSubmission, error) {
	user, err := m.repo.GetUserByID(ctx, userID)
	if err != nil {
		return model.KYCSubmission{}, err
	}
	if levelRequested != user.KYCLevel+1 || levelRequested < 1 || levelRequested > 2 {
		return model.KYCSubmission{}, ErrKYCLevelSequence
	}
	if latest, latestErr := m.repo.GetLatestKYCSubmission(ctx, userID); latestErr == nil && latest.Status == "pending" {
		return model.KYCSubmission{}, ErrKYCPending
	} else if latestErr != nil && !errors.Is(latestErr, repository.ErrKYCSubmissionNotFound) {
		return model.KYCSubmission{}, latestErr
	}
	submission := model.KYCSubmission{ID: uuid.New(), UserID: userID, LevelRequested: levelRequested, Status: "pending", Payload: payload, Provider: m.kycProvider.Name()}
	if err := m.repo.CreateKYCSubmission(ctx, submission); err != nil {
		if errors.Is(err, repository.ErrKYCSubmissionNotPending) {
			return model.KYCSubmission{}, ErrKYCPending
		}
		return model.KYCSubmission{}, err
	}
	decision, err := m.kycProvider.Verify(ctx, kycvendor.Submission{UserID: userID, LevelRequested: levelRequested, Payload: payload})
	if err != nil {
		return submission, fmt.Errorf("%w: %v", ErrKYCProvider, err)
	}
	submission.ProviderRef, submission.DecisionReason = decision.Ref, decision.Reason
	switch decision.Verdict {
	case kycvendor.VerdictApprove:
		if err := m.approveSubmission(ctx, submission, "system"); err != nil {
			return submission, err
		}
		submission.Status = "approved"
	case kycvendor.VerdictReject:
		if err := m.repo.RejectKYCSubmission(ctx, submission.ID, "provider", decision.Reason); err != nil {
			return submission, err
		}
		submission.Status = "rejected"
	case kycvendor.VerdictRefer:
		// The row remains pending until an admin decides it.
	default:
		return submission, fmt.Errorf("%w: provider returned unknown verdict %q", ErrKYCProvider, decision.Verdict)
	}
	return submission, nil
}

func (m *Module) KYC(ctx context.Context, userID uuid.UUID) (KYCStatus, error) {
	u, err := m.repo.GetUserByID(ctx, userID)
	if err != nil {
		return KYCStatus{}, err
	}
	result := KYCStatus{Level: u.KYCLevel}
	if s, err := m.repo.GetLatestKYCSubmission(ctx, userID); err == nil {
		result.Submission = &s
	} else if !errors.Is(err, repository.ErrKYCSubmissionNotFound) {
		return KYCStatus{}, err
	}
	return result, nil
}

func (m *Module) ListKYCSubmissions(ctx context.Context, status string) ([]model.KYCSubmission, error) {
	return m.repo.ListKYCSubmissions(ctx, status)
}

func (m *Module) approveSubmission(ctx context.Context, submission model.KYCSubmission, decidedBy string) error {
	err := m.repo.ApproveKYCSubmission(ctx, submission.ID, decidedBy, submission.ProviderRef, submission.DecisionReason, m.provisioner.ApplyKycTier)
	if err == nil || !errors.Is(err, repository.ErrKYCApplyTier) {
		return err
	}

	// ApproveKYCSubmission owns the fast-path transaction. It has rolled back
	// completely at this point, so this insert is intentionally a separate
	// transaction and cannot advance auth_users. A duplicate pending intent
	// is harmless when an admin click races the relay.
	// Derive the intent id from the submission so concurrent approval callers
	// converge on one durable row and return the same retry id.
	retry := model.KYCApplyRetry{
		ID:            uuid.NewSHA1(uuid.Nil, []byte("kyc-apply:"+submission.ID.String())),
		SubmissionID:  submission.ID,
		UserID:        submission.UserID,
		Level:         submission.LevelRequested,
		Status:        "pending",
		NextAttemptAt: time.Now(),
		LastError:     truncateRetryError(err),
	}
	// The request context may already be cancelled because the ledger call
	// timed out. Durable recovery must not depend on the caller staying
	// connected, so use a short detached persistence context.
	queueCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if enqueueErr := m.repo.EnqueueKYCApplyRetry(queueCtx, retry); enqueueErr != nil {
		return fmt.Errorf("auth: persist kyc apply retry: %w (original: %v)", enqueueErr, err)
	}
	kycApplyRetriesQueuedTotal.Inc()
	return &KYCApplyQueuedError{RetryID: retry.ID, Cause: err}
}

// RetryKYCApply re-runs the full limits-first approval flow for a claimed
// intent. A non-pending submission is already converged (for example an
// admin approved it manually), so it is treated as a successful no-op.
func (m *Module) RetryKYCApply(ctx context.Context, retry model.KYCApplyRetry) error {
	submission, err := m.repo.GetKYCSubmission(ctx, retry.SubmissionID)
	if err != nil {
		return err
	}
	if submission.Status != "pending" {
		return nil
	}
	err = m.approveSubmission(ctx, submission, "system-retry")
	if errors.Is(err, repository.ErrKYCSubmissionNotPending) {
		// A manual admin approval may have won the row lock after the initial
		// read. Re-read once and converge the intent to succeeded instead of
		// needlessly burning another retry.
		latest, readErr := m.repo.GetKYCSubmission(ctx, retry.SubmissionID)
		if readErr == nil && latest.Status != "pending" {
			return nil
		}
	}
	return err
}

// NewKYCApplyRetryJob wires the auth-owned relay. Keeping construction here
// means cmd/auth-service only depends on the auth facade and never reaches
// into repository internals.
func (m *Module) NewKYCApplyRetryJob(redisClient *redis.Client, logger *slog.Logger) *worker.RetryJob {
	var lock scheduler.LockProvider
	if redisClient != nil {
		instanceID, err := os.Hostname()
		if err != nil || instanceID == "" {
			instanceID = uuid.NewString()
		}
		lock = scheduler.NewRedisLock(redisClient, instanceID)
	} else {
		lock = scheduler.NewMemoryLock(2 * time.Minute)
	}
	return worker.NewRetryJob(m.repo, m, lock, logger)
}

func (m *Module) ApproveKYC(ctx context.Context, submissionID uuid.UUID, decidedBy string) error {
	s, err := m.repo.GetKYCSubmission(ctx, submissionID)
	if err != nil {
		return err
	}
	if s.Status != "pending" {
		return repository.ErrKYCSubmissionNotPending
	}
	return m.approveSubmission(ctx, s, decidedBy)
}

func (m *Module) RejectKYC(ctx context.Context, submissionID uuid.UUID, decidedBy, reason string) error {
	if reason == "" {
		return ErrValidation
	}
	return m.repo.RejectKYCSubmission(ctx, submissionID, decidedBy, reason)
}

// ─── internals ───────────────────────────────────────────────────────────────

func (m *Module) provision(ctx context.Context, userID uuid.UUID) error {
	return m.provisioner.ProvisionUser(ctx, userID, m.cfg.DefaultCurrency)
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
	if err := m.repo.InsertRefreshToken(ctx, model.RefreshToken{
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

func truncateRetryError(err error) string {
	if err == nil {
		return ""
	}
	const max = 1024
	message := err.Error()
	if len(message) > max {
		return message[:max]
	}
	return message
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

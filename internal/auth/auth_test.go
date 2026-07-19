package auth

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/bcrypt"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/internal/kycvendor/mockkyc"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

const testJWTSecret = "supersecretkeythatisatleast32chars!"

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stubProvisioner implements Provisioner without a real ledger.
type stubProvisioner struct {
	calls int
	err   error
}

func (s *stubProvisioner) ProvisionUser(_ context.Context, _ uuid.UUID, _ string) error {
	s.calls++
	return s.err
}

func (s *stubProvisioner) ApplyKycTier(_ context.Context, _ uuid.UUID, _ int) error {
	s.calls++
	return s.err
}

func testConfig() Config {
	return Config{
		JWTSecret: testJWTSecret, JWTIssuer: "seev-test",
		AccessExpiry: 15 * time.Minute, RefreshExpiry: 7 * 24 * time.Hour,
		DefaultCurrency: "IDR",
	}
}

func newTestModule(repo repository.Repository, prov Provisioner) *Module {
	return &Module{repo: repo, provisioner: prov, cfg: testConfig(), logger: discardLogger(), kycProvider: mockkyc.New()}
}

// mustHash produces a real bcrypt hash for test fixtures — MinCost keeps the
// suite fast; production cost is exercised implicitly via Register.
func mustHash(t *testing.T, password string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	require.NoError(t, err)
	return string(h)
}

func TestRegister_HappyPath_IssuesTokensAndProvisions(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	prov := &stubProvisioner{}

	repo.EXPECT().CreateUser(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, u model.User, hash string) error {
			assert.Equal(t, "user", u.Role)
			assert.Equal(t, "active", u.Status)
			assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("hunter22!")),
				"stored hash must verify against the original password")
			return nil
		})
	repo.EXPECT().InsertRefreshToken(gomock.Any(), gomock.Any()).Return(nil)

	m := newTestModule(repo, prov)
	u, pair, err := m.Register(context.Background(), "alice@example.com", "hunter22!", "Alice")
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", u.Email)
	assert.Equal(t, 1, prov.calls, "register must provision ledger accounts")
	assert.NotEmpty(t, pair.AccessToken)
	assert.NotEmpty(t, pair.RefreshToken)

	// The issued access token must satisfy the EXISTING middleware contract.
	claims, err := middleware.ParseToken(testJWTSecret, pair.AccessToken, "seev-test")
	require.NoError(t, err)
	assert.Equal(t, u.ID.String(), claims.UserID)
	assert.Equal(t, "user", claims.Role)
}

func TestRegister_DuplicateEmail_ErrEmailTaken(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	repo.EXPECT().CreateUser(gomock.Any(), gomock.Any(), gomock.Any()).Return(repository.ErrDuplicateEmail)

	m := newTestModule(repo, &stubProvisioner{})
	_, _, err := m.Register(context.Background(), "dup@example.com", "hunter22!", "")
	assert.ErrorIs(t, err, ErrEmailTaken)
}

func TestRegister_InvalidInput_400Class(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := newTestModule(repo, &stubProvisioner{})

	_, _, err := m.Register(context.Background(), "not-an-email", "hunter22!", "")
	assert.ErrorIs(t, err, ErrValidation)

	_, _, err = m.Register(context.Background(), "ok@example.com", "short", "")
	assert.ErrorIs(t, err, ErrValidation)
}

func TestRegister_ProvisionFails_RegistrationStillSucceeds(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	repo.EXPECT().CreateUser(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().InsertRefreshToken(gomock.Any(), gomock.Any()).Return(nil)

	prov := &stubProvisioner{err: context.DeadlineExceeded}
	m := newTestModule(repo, prov)
	_, pair, err := m.Register(context.Background(), "bob@example.com", "hunter22!", "")
	require.NoError(t, err, "a provision failure must not fail registration — login lazily heals it")
	assert.NotEmpty(t, pair.AccessToken)
}

func TestLogin_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID := uuid.New()
	u := model.User{ID: userID, Email: "alice@example.com", Role: "user", Status: "active"}

	repo.EXPECT().GetUserByEmail(gomock.Any(), "alice@example.com").Return(u, nil)
	repo.EXPECT().GetPasswordHash(gomock.Any(), userID).Return(mustHash(t, "hunter22!"), nil)
	repo.EXPECT().InsertRefreshToken(gomock.Any(), gomock.Any()).Return(nil)

	prov := &stubProvisioner{}
	m := newTestModule(repo, prov)
	got, pair, err := m.Login(context.Background(), "alice@example.com", "hunter22!")
	require.NoError(t, err)
	assert.Equal(t, userID, got.ID)
	assert.NotEmpty(t, pair.RefreshToken)
	assert.Equal(t, 1, prov.calls, "login must lazily re-provision (idempotent)")
}

func TestLogin_WrongPassword_ErrInvalidCredentials(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID := uuid.New()
	repo.EXPECT().GetUserByEmail(gomock.Any(), gomock.Any()).Return(
		model.User{ID: userID, Status: "active"}, nil)
	repo.EXPECT().GetPasswordHash(gomock.Any(), userID).Return(mustHash(t, "correct-password"), nil)

	m := newTestModule(repo, &stubProvisioner{})
	_, _, err := m.Login(context.Background(), "alice@example.com", "wrong-password")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestLogin_UnknownEmail_SameErrorAsWrongPassword(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	repo.EXPECT().GetUserByEmail(gomock.Any(), gomock.Any()).Return(model.User{}, repository.ErrNotFound)

	m := newTestModule(repo, &stubProvisioner{})
	_, _, err := m.Login(context.Background(), "ghost@example.com", "whatever12")
	assert.ErrorIs(t, err, ErrInvalidCredentials,
		"unknown email must be indistinguishable from wrong password")
}

func TestLogin_DisabledUser_ErrUserDisabled(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID := uuid.New()
	repo.EXPECT().GetUserByEmail(gomock.Any(), gomock.Any()).Return(
		model.User{ID: userID, Status: "disabled"}, nil)
	repo.EXPECT().GetPasswordHash(gomock.Any(), userID).Return(mustHash(t, "hunter22!"), nil)

	m := newTestModule(repo, &stubProvisioner{})
	_, _, err := m.Login(context.Background(), "off@example.com", "hunter22!")
	assert.ErrorIs(t, err, ErrUserDisabled)
}

func TestRefresh_Rotation_RevokesOldIssuesNew(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID := uuid.New()
	oldID := uuid.New()

	repo.EXPECT().GetRefreshTokenByHash(gomock.Any(), hashToken("old-token")).Return(model.RefreshToken{
		ID: oldID, UserID: userID, ExpiresAt: time.Now().Add(time.Hour),
	}, nil)
	repo.EXPECT().GetUserByID(gomock.Any(), userID).Return(
		model.User{ID: userID, Status: "active", Role: "user"}, nil)
	repo.EXPECT().InsertRefreshToken(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().RevokeRefreshToken(gomock.Any(), oldID, gomock.Not(gomock.Nil())).Return(true, nil)

	m := newTestModule(repo, &stubProvisioner{})
	_, pair, err := m.Refresh(context.Background(), "old-token")
	require.NoError(t, err)
	assert.NotEqual(t, "old-token", pair.RefreshToken, "rotation must issue a NEW opaque token")
}

func TestRefresh_ReusedRevokedToken_RevokesAllAndRejects(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID := uuid.New()
	revoked := time.Now().Add(-time.Minute)

	repo.EXPECT().GetRefreshTokenByHash(gomock.Any(), gomock.Any()).Return(model.RefreshToken{
		ID: uuid.New(), UserID: userID, ExpiresAt: time.Now().Add(time.Hour), RevokedAt: &revoked,
	}, nil)
	repo.EXPECT().RevokeAllForUser(gomock.Any(), userID).Return(nil)

	m := newTestModule(repo, &stubProvisioner{})
	_, _, err := m.Refresh(context.Background(), "stolen-token")
	assert.ErrorIs(t, err, ErrInvalidRefreshToken,
		"a replayed (already-revoked) token must be rejected AND nuke the chain")
}

func TestRefresh_ExpiredToken_Rejected(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)

	repo.EXPECT().GetRefreshTokenByHash(gomock.Any(), gomock.Any()).Return(model.RefreshToken{
		ID: uuid.New(), UserID: uuid.New(), ExpiresAt: time.Now().Add(-time.Minute),
	}, nil)

	m := newTestModule(repo, &stubProvisioner{})
	_, _, err := m.Refresh(context.Background(), "expired-token")
	assert.ErrorIs(t, err, ErrInvalidRefreshToken)
}

func TestRefresh_UnknownToken_Rejected(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	repo.EXPECT().GetRefreshTokenByHash(gomock.Any(), gomock.Any()).Return(model.RefreshToken{}, repository.ErrNotFound)

	m := newTestModule(repo, &stubProvisioner{})
	_, _, err := m.Refresh(context.Background(), "no-such-token")
	assert.ErrorIs(t, err, ErrInvalidRefreshToken)
}

func TestEnsureBootstrapAdmin_CreatesOnceThenNoop(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)

	// First call: not found -> creates with role admin.
	repo.EXPECT().GetUserByEmail(gomock.Any(), "root@example.com").Return(model.User{}, repository.ErrNotFound)
	repo.EXPECT().CreateUser(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, u model.User, _ string) error {
			assert.Equal(t, "admin", u.Role)
			return nil
		})
	// Second call: exists -> no create.
	repo.EXPECT().GetUserByEmail(gomock.Any(), "root@example.com").Return(model.User{ID: uuid.New()}, nil)

	m := newTestModule(repo, &stubProvisioner{})
	require.NoError(t, m.EnsureBootstrapAdmin(context.Background(), "root@example.com", "super-secret-pass"))
	require.NoError(t, m.EnsureBootstrapAdmin(context.Background(), "root@example.com", "super-secret-pass"))
}

func TestEnsureBootstrapAdmin_Unconfigured_Noop(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl) // no calls expected
	m := newTestModule(repo, &stubProvisioner{})
	require.NoError(t, m.EnsureBootstrapAdmin(context.Background(), "", ""))
}

func TestSubmitKYC_L1AutoApprove(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID := uuid.New()
	repo.EXPECT().GetUserByID(gomock.Any(), userID).Return(model.User{ID: userID, KYCLevel: 0}, nil)
	repo.EXPECT().GetLatestKYCSubmission(gomock.Any(), userID).Return(model.KYCSubmission{}, repository.ErrKYCSubmissionNotFound)
	var submission model.KYCSubmission
	repo.EXPECT().CreateKYCSubmission(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s model.KYCSubmission) error { submission = s; return nil })
	repo.EXPECT().ApproveKYCSubmission(gomock.Any(), gomock.Any(), "system", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ uuid.UUID, _ string, _ string, _ string, apply func(context.Context, uuid.UUID, int) error) error {
		return apply(ctx, userID, 1)
	})

	prov := &stubProvisioner{}
	m := newTestModule(repo, prov)
	got, err := m.SubmitKYC(context.Background(), userID, 1, map[string]any{"name": "Alice"})
	require.NoError(t, err)
	assert.Equal(t, submission.ID, got.ID)
	assert.Equal(t, "approved", got.Status)
	assert.Equal(t, 1, prov.calls)
}

// TestSubmitKYC_RejectVerdict_MarksRejectedNoLevelChange proves the
// provider's plain `reject` verdict (docs/plan/39 Task T3's "Test wajib"
// reject scenario) marks the submission rejected with the provider's
// reason, never touches ApplyKycTier, and leaves the user's level
// unchanged.
func TestSubmitKYC_RejectVerdict_MarksRejectedNoLevelChange(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID := uuid.New()
	repo.EXPECT().GetUserByID(gomock.Any(), userID).Return(model.User{ID: userID, KYCLevel: 0}, nil)
	repo.EXPECT().GetLatestKYCSubmission(gomock.Any(), userID).Return(model.KYCSubmission{}, repository.ErrKYCSubmissionNotFound)
	var submissionID uuid.UUID
	repo.EXPECT().CreateKYCSubmission(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s model.KYCSubmission) error { submissionID = s.ID; return nil })
	repo.EXPECT().RejectKYCSubmission(gomock.Any(), gomock.Any(), "provider", "mock verification rejected").
		DoAndReturn(func(_ context.Context, id uuid.UUID, _, _ string) error {
			assert.Equal(t, submissionID, id)
			return nil
		})

	prov := &stubProvisioner{}
	m := newTestModule(repo, prov)
	got, err := m.SubmitKYC(context.Background(), userID, 1, map[string]any{"mock_mode": mockkyc.ModeReject})
	require.NoError(t, err)
	assert.Equal(t, "rejected", got.Status)
	assert.Zero(t, prov.calls, "a rejected submission must never call ApplyKycTier")
}

func TestSubmitKYC_RejectsLevelJumpAndDuplicatePending(t *testing.T) {
	userID := uuid.New()
	t.Run("level jump", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := repository.NewMockRepository(ctrl)
		repo.EXPECT().GetUserByID(gomock.Any(), userID).Return(model.User{ID: userID, KYCLevel: 0}, nil)
		_, err := newTestModule(repo, &stubProvisioner{}).SubmitKYC(context.Background(), userID, 2, nil)
		assert.ErrorIs(t, err, ErrKYCLevelSequence)
	})
	t.Run("pending duplicate", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := repository.NewMockRepository(ctrl)
		repo.EXPECT().GetUserByID(gomock.Any(), userID).Return(model.User{ID: userID, KYCLevel: 0}, nil)
		repo.EXPECT().GetLatestKYCSubmission(gomock.Any(), userID).Return(model.KYCSubmission{Status: "pending"}, nil)
		_, err := newTestModule(repo, &stubProvisioner{}).SubmitKYC(context.Background(), userID, 1, nil)
		assert.ErrorIs(t, err, ErrKYCPending)
	})
}

func TestSubmitKYC_L2ReferThenAdminApprove(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID, submissionID := uuid.New(), uuid.New()
	repo.EXPECT().GetUserByID(gomock.Any(), userID).Return(model.User{ID: userID, KYCLevel: 1}, nil)
	repo.EXPECT().GetLatestKYCSubmission(gomock.Any(), userID).Return(model.KYCSubmission{}, repository.ErrKYCSubmissionNotFound)
	repo.EXPECT().CreateKYCSubmission(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s model.KYCSubmission) error { submissionID = s.ID; return nil })
	prov := &stubProvisioner{}
	m := newTestModule(repo, prov)
	submission, err := m.SubmitKYC(context.Background(), userID, 2, map[string]any{"mock_mode": mockkyc.ModeApprove})
	require.NoError(t, err)
	assert.Equal(t, "pending", submission.Status)
	assert.Zero(t, prov.calls)

	repo.EXPECT().GetKYCSubmission(gomock.Any(), submissionID).Return(submission, nil)
	repo.EXPECT().ApproveKYCSubmission(gomock.Any(), submissionID, "admin-1", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ uuid.UUID, _ string, _ string, _ string, apply func(context.Context, uuid.UUID, int) error) error {
		return apply(ctx, userID, 2)
	})
	require.NoError(t, m.ApproveKYC(context.Background(), submissionID, "admin-1"))
	assert.Equal(t, 1, prov.calls)
}

func TestSubmitKYC_ApplyTierFailureLeavesApprovalPending(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID := uuid.New()
	repo.EXPECT().GetUserByID(gomock.Any(), userID).Return(model.User{ID: userID, KYCLevel: 0}, nil)
	repo.EXPECT().GetLatestKYCSubmission(gomock.Any(), userID).Return(model.KYCSubmission{}, repository.ErrKYCSubmissionNotFound)
	repo.EXPECT().CreateKYCSubmission(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().ApproveKYCSubmission(gomock.Any(), gomock.Any(), "system", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ uuid.UUID, _ string, _ string, _ string, apply func(context.Context, uuid.UUID, int) error) error {
		return fmt.Errorf("%w: %w", repository.ErrKYCApplyTier, apply(ctx, userID, 1))
	})
	repo.EXPECT().EnqueueKYCApplyRetry(gomock.Any(), gomock.Any()).Return(nil)

	m := newTestModule(repo, &stubProvisioner{err: context.DeadlineExceeded})
	_, err := m.SubmitKYC(context.Background(), userID, 1, nil)
	assert.ErrorIs(t, err, ErrKYCApplyQueued)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestDowngradeKYC_LimitsFirst(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID := uuid.New()
	repo.EXPECT().GetUserByID(gomock.Any(), userID).Return(model.User{ID: userID, KYCLevel: 2}, nil)
	var order []string
	repo.EXPECT().DowngradeKYCLevel(gomock.Any(), userID, 0, "admin-1", "sanctions review", gomock.Any()).DoAndReturn(
		func(ctx context.Context, id uuid.UUID, level int, _ string, _ string, apply func(context.Context, uuid.UUID, int) error) error {
			order = append(order, "limits")
			require.NoError(t, apply(ctx, id, level))
			order = append(order, "auth")
			return nil
		})

	m := newTestModule(repo, &stubProvisioner{})
	require.NoError(t, m.DowngradeKYC(context.Background(), userID, 0, "admin-1", "sanctions review"))
	assert.Equal(t, []string{"limits", "auth"}, order)
}

func TestDowngradeKYC_ApplyFailureQueuesDurableIntent(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID := uuid.New()
	repo.EXPECT().GetUserByID(gomock.Any(), userID).Return(model.User{ID: userID, KYCLevel: 2}, nil)
	repo.EXPECT().DowngradeKYCLevel(gomock.Any(), userID, 1, "admin-1", "policy review", gomock.Any()).DoAndReturn(
		func(ctx context.Context, id uuid.UUID, level int, _, _ string, apply func(context.Context, uuid.UUID, int) error) error {
			return fmt.Errorf("%w: %w", repository.ErrKYCApplyTier, apply(ctx, id, level))
		})
	repo.EXPECT().EnqueueKYCApplyRetry(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, retry model.KYCApplyRetry) error {
		assert.Equal(t, "downgrade", retry.Direction)
		assert.Equal(t, 1, retry.Level)
		assert.Equal(t, userID, retry.UserID)
		assert.Equal(t, "admin-1", retry.DecidedBy)
		return nil
	})

	m := newTestModule(repo, &stubProvisioner{err: context.DeadlineExceeded})
	err := m.DowngradeKYC(context.Background(), userID, 1, "admin-1", "policy review")
	assert.ErrorIs(t, err, ErrKYCApplyQueued)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

type sanctionsCheckerStub struct{ verdict fraudcheck.Verdict }

func (s sanctionsCheckerStub) CheckWithSubject(context.Context, string, string, uuid.UUID, decimal.Decimal, string, string, string) (fraudcheck.Verdict, error) {
	return s.verdict, nil
}

func TestSubmitKYC_SanctionsBlockRejectsBeforeProvider(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repository.NewMockRepository(ctrl)
	userID := uuid.New()
	repo.EXPECT().GetUserByID(gomock.Any(), userID).Return(model.User{ID: userID, KYCLevel: 0}, nil)
	repo.EXPECT().GetLatestKYCSubmission(gomock.Any(), userID).Return(model.KYCSubmission{}, repository.ErrKYCSubmissionNotFound)
	var submissionID uuid.UUID
	repo.EXPECT().CreateKYCSubmission(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s model.KYCSubmission) error { submissionID = s.ID; return nil })
	repo.EXPECT().RejectKYCSubmission(gomock.Any(), gomock.Any(), "sanctions", "sanctions match").DoAndReturn(func(_ context.Context, id uuid.UUID, _, _ string) error {
		assert.Equal(t, submissionID, id)
		return nil
	})

	m := newTestModule(repo, &stubProvisioner{})
	m.sanctionsChecker = sanctionsCheckerStub{verdict: fraudcheck.Verdict{Block: true, Reason: "sanctions match"}}
	got, err := m.SubmitKYC(context.Background(), userID, 1, map[string]any{"name": "Jane Doe"})
	require.NoError(t, err)
	assert.Equal(t, "rejected", got.Status)
}

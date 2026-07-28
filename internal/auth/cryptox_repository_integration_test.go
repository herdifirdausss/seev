//go:build integration

// Proves docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T2.3's K2/K3
// encryption (contract-migrated by "A8 T2.5b" — no plaintext fallback
// remains) for auth_users.email/full_name and kyc_submissions.payload end
// to end against a real Postgres: ciphertext round-trip, normalized email
// lookup/uniqueness via the deterministic digest, a nil ring/lookup being
// refused at construction rather than silently degrading, and existing
// business/KYC behavior (ListKYCRescreenSubjects) staying correct once
// payload is encrypted. Reuses setupAuthTestDB (auth_integration_test.go,
// same package).
package auth_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/pkg/cryptox"
)

func testRing(t *testing.T) *cryptox.Ring {
	t.Helper()
	require.NotNil(t, cryptoxTestRing)
	return cryptoxTestRing
}

func testLookupKey(t *testing.T) *cryptox.LookupKey {
	t.Helper()
	require.NotNil(t, cryptoxTestLookup)
	return cryptoxTestLookup
}

// cryptoxTestRing/cryptoxTestLookup are package-level (no *testing.T
// needed) so setup helpers like newAuthModule — which auth.NewModule now
// REQUIRES a real ring/lookup for, "A8 T2.5b" having removed the
// nil-ring-tolerant construction path entirely — can use them without
// threading t through every call site. testRing(t)/testLookupKey(t) above
// stay as the test-facing accessors other test files already call.
var (
	cryptoxTestRing   = mustBuildTestRing()
	cryptoxTestLookup = mustBuildTestLookupKey()
)

func mustBuildTestRing() *cryptox.Ring {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	if err != nil {
		panic(err)
	}
	return ring
}

func mustBuildTestLookupKey() *cryptox.LookupKey {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 100)
	}
	lk, err := cryptox.NewLookupKey(key)
	if err != nil {
		panic(err)
	}
	return lk
}

func TestUserRepository_CreateAndGet_RoundTripsThroughCiphertext(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := repository.NewUserRepository(db, testRing(t), testLookupKey(t))
	ctx := context.Background()

	u := model.User{ID: uuid.New(), Email: "Mia@Example.Test", FullName: "Mia Wallace", Role: "user", Status: "active"}
	require.NoError(t, repo.CreateUser(ctx, u, "hash"))

	var ciphertext []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT email_ciphertext FROM auth_users WHERE id = $1`, u.ID).Scan(&ciphertext))
	require.NotEmpty(t, ciphertext)
	require.NotContains(t, string(ciphertext), "Mia@Example.Test")

	got, err := repo.GetUserByID(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, "Mia@Example.Test", got.Email)
	require.Equal(t, "Mia Wallace", got.FullName)
}

func TestUserRepository_GetUserByEmail_NormalizedLookupViaDigest(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := repository.NewUserRepository(db, testRing(t), testLookupKey(t))
	ctx := context.Background()

	u := model.User{ID: uuid.New(), Email: "Noah@Example.Test", FullName: "Noah", Role: "user", Status: "active"}
	require.NoError(t, repo.CreateUser(ctx, u, "hash"))

	// Case-insensitive, matching the original lower(email) semantics —
	// proven here via the lookup digest path, not the plaintext fallback.
	got, err := repo.GetUserByEmail(ctx, "noah@example.test")
	require.NoError(t, err)
	require.Equal(t, u.ID, got.ID)

	got, err = repo.GetUserByEmail(ctx, "NOAH@EXAMPLE.TEST")
	require.NoError(t, err)
	require.Equal(t, u.ID, got.ID)
}

func TestUserRepository_DuplicateEmail_RejectedViaDigestUniqueness(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := repository.NewUserRepository(db, testRing(t), testLookupKey(t))
	ctx := context.Background()

	require.NoError(t, repo.CreateUser(ctx, model.User{ID: uuid.New(), Email: "dup@example.test", Role: "user", Status: "active"}, "hash"))
	err := repo.CreateUser(ctx, model.User{ID: uuid.New(), Email: "DUP@example.test", Role: "user", Status: "active"}, "hash")
	require.ErrorIs(t, err, repository.ErrDuplicateEmail)
}

// TestUserRepository_NilRingOrLookup_PanicsAtConstruction is "A8 T2.5b"'s
// own required test: once auth_users.email/full_name has no plaintext
// column, a missing ring or lookup key can never degrade gracefully — it
// must fail loudly at construction, not nil-pointer somewhere inside a
// live request.
func TestUserRepository_NilRingOrLookup_PanicsAtConstruction(t *testing.T) {
	db := setupAuthTestDB(t)
	require.Panics(t, func() { repository.NewUserRepository(db, nil, testLookupKey(t)) })
	require.Panics(t, func() { repository.NewUserRepository(db, testRing(t), nil) })
	require.Panics(t, func() { repository.NewUserRepository(db, nil, nil) })
}

// TestKYCRepository_NilRing_PanicsAtConstruction mirrors the user
// repository's own construction-time fail-closed behavior.
func TestKYCRepository_NilRing_PanicsAtConstruction(t *testing.T) {
	db := setupAuthTestDB(t)
	require.Panics(t, func() { repository.NewKYCRepository(db, nil) })
}

func TestKYCRepository_CreateSubmission_EncryptsPayloadWithoutPlaintextProjection(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()

	userID := uuid.New()
	insertTestUser(t, db, userID)
	repo := repository.NewKYCRepository(db, testRing(t))

	sub := model.KYCSubmission{
		ID: uuid.New(), UserID: userID, LevelRequested: 1, Provider: "test",
		Payload: map[string]any{"name": "Mia Wallace", "birth_date": "1990-01-01"},
	}
	require.NoError(t, repo.CreateKYCSubmission(ctx, sub))

	var ciphertext []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT payload_ciphertext FROM kyc_submissions WHERE id = $1`, sub.ID).Scan(&ciphertext))
	require.NotEmpty(t, ciphertext)
	require.NotContains(t, string(ciphertext), "Mia Wallace")

	got, err := repo.GetKYCSubmission(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, "Mia Wallace", got.Payload["name"])
	require.Equal(t, "1990-01-01", got.Payload["birth_date"])

	var eligible bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT rescreen_eligible FROM kyc_submissions WHERE id = $1`, sub.ID).Scan(&eligible))
	require.True(t, eligible)
}

// TestKYCRepository_ListRescreenSubjects_WorksAgainstEncryptedPayload proves
// docs/roadmap/archive/51 T2.3's own required test: "existing business, KYC ... behavior
// remains correct" — specifically the sanctions rescreen job's own list
// query, which now decrypts the allowlisted fields from payload_ciphertext.
func TestKYCRepository_ListRescreenSubjects_WorksAgainstEncryptedPayload(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()
	repo := repository.NewKYCRepository(db, testRing(t))

	approvedUser := uuid.New()
	insertTestUser(t, db, approvedUser)
	approvedSub := model.KYCSubmission{
		ID: uuid.New(), UserID: approvedUser, LevelRequested: 1, Provider: "test",
		Payload: map[string]any{"full_name": "Noah", "birth_date": "1985-05-05"},
	}
	require.NoError(t, repo.CreateKYCSubmission(ctx, approvedSub))
	_, err := db.ExecContext(ctx, `UPDATE kyc_submissions SET status = 'approved', decided_at = now() WHERE id = $1`, approvedSub.ID)
	require.NoError(t, err)

	pendingUser := uuid.New()
	insertTestUser(t, db, pendingUser)
	pendingSub := model.KYCSubmission{
		ID: uuid.New(), UserID: pendingUser, LevelRequested: 1, Provider: "test",
		Payload: map[string]any{"name": "Should Not Appear"},
	}
	require.NoError(t, repo.CreateKYCSubmission(ctx, pendingSub))

	subjects, err := repo.ListKYCRescreenSubjects(ctx, 100)
	require.NoError(t, err)
	require.Len(t, subjects, 1, "only the approved submission is rescreen-eligible")
	require.Equal(t, approvedUser, subjects[0].UserID)
	require.Equal(t, "Noah", subjects[0].Name)
	require.Equal(t, "1985-05-05", subjects[0].BirthDate)
}

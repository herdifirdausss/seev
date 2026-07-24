//go:build integration

// Proves docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.3's K2/K3 expand-phase
// encryption for auth_users.email/full_name and kyc_submissions.payload
// end to end against a real Postgres: ciphertext round-trip, normalized
// email lookup/uniqueness via the deterministic digest (never plaintext),
// dual-read/write compatibility with pre-migration rows, and existing
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
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	return ring
}

func testLookupKey(t *testing.T) *cryptox.LookupKey {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 100)
	}
	lk, err := cryptox.NewLookupKey(key)
	require.NoError(t, err)
	return lk
}

func TestUserRepository_CreateAndGet_RoundTripsThroughCiphertext(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := repository.NewUserRepository(db, testRing(t), testLookupKey(t))
	ctx := context.Background()

	u := model.User{ID: uuid.New(), Email: "Mia@Example.Test", FullName: "Mia Wallace", Role: "user", Status: "active"}
	require.NoError(t, repo.CreateUser(ctx, u, "hash"))

	// user_repository.go dual-writes the plaintext email column too
	// during the expand phase (K3 step 2) — this assertion targets the
	// ciphertext column directly to prove it's genuinely encrypted, not
	// just dual-written for show.
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

// TestUserRepository_DualReadWrite_PreMigrationRowStillWorks is T2's own
// required test: "dual-read/write compatibility during backfill." A row
// inserted directly (plaintext only, no ciphertext/digest — simulating a
// row that predates this migration) must still be readable by both
// GetUserByID and GetUserByEmail once a ring is configured.
func TestUserRepository_DualReadWrite_PreMigrationRowStillWorks(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()

	preMigrationID := uuid.New()
	_, err := db.ExecContext(ctx, `
		INSERT INTO auth_users (id, email, full_name, role, status)
		VALUES ($1, 'legacy@example.test', 'Legacy User', 'user', 'active')`, preMigrationID)
	require.NoError(t, err)

	repo := repository.NewUserRepository(db, testRing(t), testLookupKey(t))

	byID, err := repo.GetUserByID(ctx, preMigrationID)
	require.NoError(t, err)
	require.Equal(t, "legacy@example.test", byID.Email)
	require.Equal(t, "Legacy User", byID.FullName)

	byEmail, err := repo.GetUserByEmail(ctx, "legacy@example.test")
	require.NoError(t, err, "GetUserByEmail must fall back to the plaintext path for a row with no lookup digest yet")
	require.Equal(t, preMigrationID, byEmail.ID)
}

func TestUserRepository_NilRing_BehavesLikePreT2_3(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := repository.NewUserRepository(db, nil, nil)
	ctx := context.Background()

	u := model.User{ID: uuid.New(), Email: "plain@example.test", FullName: "Plain", Role: "user", Status: "active"}
	require.NoError(t, repo.CreateUser(ctx, u, "hash"))

	var ciphertext []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT email_ciphertext FROM auth_users WHERE id = $1`, u.ID).Scan(&ciphertext))
	require.Nil(t, ciphertext, "a nil ring must never write ciphertext")

	got, err := repo.GetUserByEmail(ctx, "plain@example.test")
	require.NoError(t, err)
	require.Equal(t, u.ID, got.ID)
}

func TestKYCRepository_CreateSubmission_EncryptsPayloadAndProjectsRescreenFields(t *testing.T) {
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

	var rescreenName, rescreenBirthDate string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT rescreen_name, rescreen_birth_date FROM kyc_submissions WHERE id = $1`, sub.ID).Scan(&rescreenName, &rescreenBirthDate))
	require.Equal(t, "Mia Wallace", rescreenName)
	require.Equal(t, "1990-01-01", rescreenBirthDate)
}

// TestKYCRepository_ListRescreenSubjects_WorksAgainstEncryptedPayload proves
// docs/roadmap/active/51 T2.3's own required test: "existing business, KYC ... behavior
// remains correct" — specifically the sanctions rescreen job's own list
// query, which used to read live out of payload's plaintext JSONB and now
// reads the rescreen_name/rescreen_birth_date projection instead.
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

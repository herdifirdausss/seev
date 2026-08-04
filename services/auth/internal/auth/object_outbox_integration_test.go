//go:build integration

// Proves docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T1.6's object-delete outbox
// (internal/platform/lifecycle/objectoutbox) end to end against real Postgres, using kyc_documents
// as the concrete ref_table: enqueue is idempotent, a successful drain
// marks both the outbox row 'done' and kyc_documents.deleted_at, a store
// outage leaves both untouched and is retried, and the outbox table itself
// has no DELETE grant (append-only audit, K4's philosophy applied here
// too).
package auth_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/lifecycle/objectoutbox"
	"github.com/herdifirdausss/seev/services/auth/internal/auth/model"
	"github.com/herdifirdausss/seev/services/auth/internal/repository"
)

// fakeObjectStore is an in-memory objectoutbox.Store — this repo has no
// production object-store adapter wired yet (services/auth.DocumentStore
// is deliberately an interface with nothing implementing it in-tree; see
// services/auth/internal/auth/documents.go's own comment), so this is the closest
// equivalent to a real store outage/recovery a test can drive directly.
type fakeObjectStore struct {
	mu       sync.Mutex
	deleted  map[string]bool
	failNext bool
}

func (s *fakeObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext {
		return errors.New("object store unreachable")
	}
	if s.deleted == nil {
		s.deleted = map[string]bool{}
	}
	s.deleted[key] = true
	return nil
}

func (s *fakeObjectStore) wasDeleted(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleted[key]
}

var kycOutboxTarget = objectoutbox.Target{
	RefTable:          "kyc_documents",
	MetadataUpdateSQL: `UPDATE kyc_documents SET deleted_at = now() WHERE id = $1`,
}

func insertKYCDocumentForOutboxTest(t *testing.T, db *database.DBSQL, userID uuid.UUID) (docID uuid.UUID, objectKey string) {
	t.Helper()
	ctx := context.Background()

	submissionID := uuid.New()
	insertTestUser(t, db, userID)
	require.NoError(t, repository.NewKYCRepository(db, cryptoxTestRing).CreateKYCSubmission(ctx, model.KYCSubmission{
		ID: submissionID, UserID: userID, LevelRequested: 1, Provider: "test", Payload: map[string]any{},
	}))
	_, err := db.ExecContext(ctx, `UPDATE kyc_submissions SET status = 'approved' WHERE id = $1`, submissionID)
	require.NoError(t, err)

	docID = uuid.New()
	objectKey = "kyc/" + userID.String() + "/" + docID.String()
	_, err = db.ExecContext(ctx, `
		INSERT INTO kyc_documents (id, submission_id, user_id, object_key, sha256, size_bytes, content_type)
		VALUES ($1, $2, $3, $4, $5, 100, 'application/pdf')`,
		docID, submissionID, userID, objectKey, "0000000000000000000000000000000000000000000000000000000000000000"[:64])
	require.NoError(t, err)
	return docID, objectKey
}

func TestObjectOutbox_Enqueue_IsIdempotent(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()
	docID, objectKey := insertKYCDocumentForOutboxTest(t, db, uuid.New())

	require.NoError(t, objectoutbox.Enqueue(ctx, db, "auth", "kyc_documents", docID, objectKey))
	require.NoError(t, objectoutbox.Enqueue(ctx, db, "auth", "kyc_documents", docID, objectKey))

	var count int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM auth_object_delete_outbox WHERE ref_table = 'kyc_documents' AND ref_id = $1`, docID).Scan(&count))
	require.Equal(t, 1, count, "enqueueing the same ref twice must not duplicate the outbox row")
}

func TestObjectOutbox_SuccessfulDrain_MarksMetadataDeleted(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()
	docID, objectKey := insertKYCDocumentForOutboxTest(t, db, uuid.New())
	require.NoError(t, objectoutbox.Enqueue(ctx, db, "auth", "kyc_documents", docID, objectKey))

	store := &fakeObjectStore{}
	worker, err := objectoutbox.NewWorker("auth", db, store, []objectoutbox.Target{kycOutboxTarget})
	require.NoError(t, err)

	processed, failed, err := worker.ProcessOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Equal(t, 0, failed)
	require.True(t, store.wasDeleted(objectKey))

	var deletedAt *string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT deleted_at::text FROM kyc_documents WHERE id = $1`, docID).Scan(&deletedAt))
	require.NotNil(t, deletedAt, "kyc_documents.deleted_at must be set once the store confirms deletion")

	var status string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM auth_object_delete_outbox WHERE ref_id = $1`, docID).Scan(&status))
	require.Equal(t, "done", status)
}

// TestObjectOutbox_StoreOutage_PreservesMetadataAndRetries is T1's own
// required test against a real database: "object outage preserves
// metadata and retries deletion."
func TestObjectOutbox_StoreOutage_PreservesMetadataAndRetries(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()
	docID, objectKey := insertKYCDocumentForOutboxTest(t, db, uuid.New())
	require.NoError(t, objectoutbox.Enqueue(ctx, db, "auth", "kyc_documents", docID, objectKey))

	store := &fakeObjectStore{failNext: true}
	worker, err := objectoutbox.NewWorker("auth", db, store, []objectoutbox.Target{kycOutboxTarget})
	require.NoError(t, err)

	processed, failed, err := worker.ProcessOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, processed)
	require.Equal(t, 1, failed)

	var deletedAt *string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT deleted_at::text FROM kyc_documents WHERE id = $1`, docID).Scan(&deletedAt))
	require.Nil(t, deletedAt, "a store outage must never let metadata claim the object was removed")

	var status string
	var attempts int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status, attempts FROM auth_object_delete_outbox WHERE ref_id = $1`, docID).Scan(&status, &attempts))
	require.Equal(t, "pending", status, "a failed row must go back to pending, never stay stuck 'processing'")
	require.Equal(t, 1, attempts)

	store.failNext = false
	processed, failed, err = worker.ProcessOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Equal(t, 0, failed)
	require.True(t, store.wasDeleted(objectKey))

	require.NoError(t, db.QueryRowContext(ctx, `SELECT deleted_at::text FROM kyc_documents WHERE id = $1`, docID).Scan(&deletedAt))
	require.NotNil(t, deletedAt, "the retry must complete the deletion once the store recovers")
}

func TestObjectOutbox_DirectDeleteForbidden(t *testing.T) {
	ownerDB, ownerCfg := setupAuthTestDBWithConfig(t)
	ctx := context.Background()

	const appPassword = "app-test-pw"
	_, err := ownerDB.ExecContext(ctx, `CREATE ROLE test_outbox_app_service LOGIN PASSWORD '`+appPassword+`'`)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, `GRANT app_service TO test_outbox_app_service`)
	require.NoError(t, err)

	appCfg := ownerCfg
	appCfg.User, appCfg.Password = "test_outbox_app_service", appPassword
	appDB, err := database.New(ctx, appCfg.Pkg())
	require.NoError(t, err)
	t.Cleanup(func() { _ = appDB.Close() })

	_, err = appDB.ExecContext(ctx, `DELETE FROM auth_object_delete_outbox`)
	require.Error(t, err, "app_service must never be able to DELETE outbox rows — done rows are a permanent audit trail (K4)")
}

package objectoutbox

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/platform/database"
)

func newMockDB(t *testing.T) (*database.DBSQL, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return database.NewFromSQL(sqlDB, database.Config{}), mock
}

var kycTarget = Target{RefTable: "kyc_documents", MetadataUpdateSQL: `UPDATE kyc_documents SET deleted_at = now() WHERE id = $1`}

type fakeStore struct {
	deleteFunc func(ctx context.Context, key string) error
	calls      []string
}

func (f *fakeStore) Delete(ctx context.Context, key string) error {
	f.calls = append(f.calls, key)
	if f.deleteFunc != nil {
		return f.deleteFunc(ctx, key)
	}
	return nil
}

func TestNewWorker_RejectsMissingRequiredFields(t *testing.T) {
	db, _ := newMockDB(t)
	store := &fakeStore{}

	_, err := NewWorker("", db, store, []Target{kycTarget})
	require.Error(t, err)

	_, err = NewWorker("auth", nil, store, []Target{kycTarget})
	require.Error(t, err)

	_, err = NewWorker("auth", db, nil, []Target{kycTarget})
	require.Error(t, err)
}

func TestNewWorker_RejectsDuplicateTarget(t *testing.T) {
	db, _ := newMockDB(t)
	_, err := NewWorker("auth", db, &fakeStore{}, []Target{kycTarget, kycTarget})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate target")
}

func TestProcessOnce_SuccessfulDelete_MarksDoneAndUpdatesMetadata(t *testing.T) {
	db, mock := newMockDB(t)
	store := &fakeStore{}
	w, err := NewWorker("auth", db, store, []Target{kycTarget})
	require.NoError(t, err)

	rowID := uuid.New()
	refID := uuid.New()

	mock.ExpectQuery(`WITH claimed AS \(\s*UPDATE auth_object_delete_outbox`).
		WithArgs(DefaultBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id", "ref_table", "ref_id", "object_key", "attempts"}).
			AddRow(rowID, "kyc_documents", refID, "kyc/user/doc1", 0))

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE auth_object_delete_outbox SET status = 'done'`).
		WithArgs(rowID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE kyc_documents SET deleted_at = now\(\) WHERE id = \$1`).
		WithArgs(refID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	processed, failed, err := w.ProcessOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, 0, failed)
	assert.Equal(t, []string{"kyc/user/doc1"}, store.calls)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestProcessOnce_StoreOutage_PreservesMetadataAndRetries is T1's own
// required test: "object outage preserves metadata and retries deletion."
// A failing Store.Delete must never reach the metadata UPDATE — the row
// goes back to 'pending' (never 'done'), and a later successful retry
// still completes the deletion.
func TestProcessOnce_StoreOutage_PreservesMetadataAndRetries(t *testing.T) {
	db, mock := newMockDB(t)
	storeErr := errors.New("object store unreachable")
	store := &fakeStore{deleteFunc: func(context.Context, string) error { return storeErr }}
	w, err := NewWorker("auth", db, store, []Target{kycTarget})
	require.NoError(t, err)

	rowID := uuid.New()
	refID := uuid.New()

	mock.ExpectQuery(`WITH claimed AS`).
		WithArgs(DefaultBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id", "ref_table", "ref_id", "object_key", "attempts"}).
			AddRow(rowID, "kyc_documents", refID, "kyc/user/doc1", 0))
	// Only the mark-failed UPDATE runs — no BeginTx, no metadata UPDATE:
	// the outage must never let metadata claim the object was removed.
	mock.ExpectExec(`UPDATE auth_object_delete_outbox SET status = 'pending', attempts = attempts \+ 1`).
		WithArgs(rowID, "store delete: "+storeErr.Error()).WillReturnResult(sqlmock.NewResult(0, 1))

	processed, failed, err := w.ProcessOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, processed)
	assert.Equal(t, 1, failed)
	require.NoError(t, mock.ExpectationsWereMet())

	// Retry: the store recovers, and this time the row is claimed and
	// drained through to completion, proving the earlier failure did not
	// leave it stuck.
	store.deleteFunc = nil
	mock.ExpectQuery(`WITH claimed AS`).
		WithArgs(DefaultBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id", "ref_table", "ref_id", "object_key", "attempts"}).
			AddRow(rowID, "kyc_documents", refID, "kyc/user/doc1", 1))
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE auth_object_delete_outbox SET status = 'done'`).
		WithArgs(rowID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE kyc_documents SET deleted_at = now\(\) WHERE id = \$1`).
		WithArgs(refID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	processed, failed, err = w.ProcessOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, 0, failed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProcessOnce_UnknownRefTable_MarksFailedNotDone(t *testing.T) {
	db, mock := newMockDB(t)
	store := &fakeStore{}
	w, err := NewWorker("auth", db, store, []Target{kycTarget})
	require.NoError(t, err)

	rowID := uuid.New()
	refID := uuid.New()

	mock.ExpectQuery(`WITH claimed AS`).
		WithArgs(DefaultBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id", "ref_table", "ref_id", "object_key", "attempts"}).
			AddRow(rowID, "unregistered_table", refID, "some/key", 0))
	mock.ExpectExec(`UPDATE auth_object_delete_outbox SET status = 'pending', attempts = attempts \+ 1`).
		WithArgs(rowID, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))

	processed, failed, err := w.ProcessOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, processed)
	assert.Equal(t, 1, failed)
	assert.Empty(t, store.calls, "an unregistered ref_table must never reach the store")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnqueue_UsesOnConflictDoNothing(t *testing.T) {
	db, mock := newMockDB(t)
	refID := uuid.New()

	mock.ExpectExec(`INSERT INTO auth_object_delete_outbox .* ON CONFLICT \(ref_table, ref_id\) DO NOTHING`).
		WithArgs(sqlmock.AnyArg(), "kyc_documents", refID, "kyc/user/doc1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := Enqueue(context.Background(), db, "auth", "kyc_documents", refID, "kyc/user/doc1")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnqueue_RejectsInvalidOwner(t *testing.T) {
	db, _ := newMockDB(t)
	err := Enqueue(context.Background(), db, "DROP TABLE", "kyc_documents", uuid.New(), "key")
	require.Error(t, err)
}

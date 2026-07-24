//go:build integration

// Proves docs/roadmap/active/51-a8-data-lifecycle-privacy.md T4's (K9) authenticated user
// export end to end against a real Postgres: IDOR-safe ownership,
// password re-verification, disabled-user rejection, duplicate-request
// idempotency, the built archive containing the subject's own data (and
// no password/token/internal fields), encryption at rest with a wrong-KEK
// failure, and one-time download / TTL-expiry both draining the object
// idempotently. Reuses setupAuthTestDB/newAuthModule
// (auth_integration_test.go) and testRing (cryptox_repository_integration_test.go),
// same package.
package auth_test

import (
	"archive/zip"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/auth"
	"github.com/herdifirdausss/seev/pkg/cryptox"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/objectoutbox"
)

// fakeDocStore is an in-memory docs/roadmap/active/51-a8-data-lifecycle-privacy.md auth.DocumentStore —
// this environment has no real MinIO/S3, matching every other T2/T4
// integration test's own "no production object-store adapter exists yet"
// reality (see internal/auth/documents.go's own long-standing comment).
type fakeDocStore struct{ objects map[string][]byte }

func newFakeDocStore() *fakeDocStore { return &fakeDocStore{objects: map[string][]byte{}} }

func (f *fakeDocStore) Put(_ context.Context, key string, data []byte, _ string) error {
	f.objects[key] = append([]byte(nil), data...)
	return nil
}
func (f *fakeDocStore) Get(_ context.Context, key string) ([]byte, error) {
	data, ok := f.objects[key]
	if !ok {
		return nil, auth.ErrDocumentStorageUnavailable
	}
	return data, nil
}
func (f *fakeDocStore) Delete(_ context.Context, key string) error {
	delete(f.objects, key)
	return nil
}

func setupExportModule(t *testing.T) (*auth.Module, *database.DBSQL, *fakeDocStore, *cryptox.Ring) {
	t.Helper()
	db := setupAuthTestDB(t)
	m, _ := newAuthModule(db)
	store := newFakeDocStore()
	ring := testRing(t)
	m.SetDocumentStore(store)
	m.SetExportKeyRing(ring)
	return m, db, store, ring
}

func registerTestUser(t *testing.T, m *auth.Module, email, password string) uuid.UUID {
	t.Helper()
	return registerTestUserNamed(t, m, email, password, "Test User")
}

func registerTestUserNamed(t *testing.T, m *auth.Module, email, password, fullName string) uuid.UUID {
	t.Helper()
	u, _, err := m.Register(context.Background(), email, password, fullName)
	require.NoError(t, err)
	return u.ID
}

func TestPrivacyExport_RequestExport_MissingPassword(t *testing.T) {
	m, _, _, _ := setupExportModule(t)
	userID := registerTestUser(t, m, "alice@example.test", "hunter22!")

	_, err := m.RequestExport(context.Background(), userID, "wrong-password")
	require.ErrorIs(t, err, auth.ErrInvalidCredentials)
}

func TestPrivacyExport_RequestExport_DisabledUser(t *testing.T) {
	m, db, _, _ := setupExportModule(t)
	userID := registerTestUser(t, m, "bob@example.test", "hunter22!")
	_, err := db.ExecContext(context.Background(), `UPDATE auth_users SET status = 'disabled' WHERE id = $1`, userID)
	require.NoError(t, err)

	_, err = m.RequestExport(context.Background(), userID, "hunter22!")
	require.ErrorIs(t, err, auth.ErrUserDisabled)
}

// TestPrivacyExport_RequestExport_DuplicateReturnsSameActiveRequest is
// T4's own required test: "duplicate request."
func TestPrivacyExport_RequestExport_DuplicateReturnsSameActiveRequest(t *testing.T) {
	m, _, _, _ := setupExportModule(t)
	userID := registerTestUser(t, m, "carol@example.test", "hunter22!")
	ctx := context.Background()

	first, err := m.RequestExport(ctx, userID, "hunter22!")
	require.NoError(t, err)
	second, err := m.RequestExport(ctx, userID, "hunter22!")
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID, "a second request while one is still active must return the SAME request, not create a new one")
}

// TestPrivacyExport_GetExportStatus_CrossUserIDOR is T4's own required
// test: "cross-user IDOR attempts."
func TestPrivacyExport_GetExportStatus_CrossUserIDOR(t *testing.T) {
	m, _, _, _ := setupExportModule(t)
	ctx := context.Background()
	owner := registerTestUser(t, m, "owner@example.test", "hunter22!")
	attacker := registerTestUser(t, m, "attacker@example.test", "hunter22!")

	req, err := m.RequestExport(ctx, owner, "hunter22!")
	require.NoError(t, err)

	_, err = m.GetExportStatus(ctx, attacker, req.ID)
	require.ErrorIs(t, err, auth.ErrExportNotFound, "another user's export must be reported not-found, never a distinct forbidden — no existence disclosure")

	_, err = m.GetExportStatus(ctx, owner, req.ID)
	require.NoError(t, err)
}

// TestPrivacyExport_FullLifecycle_ContainsOwnDataOnlyAndNoSecrets is T4's
// own required tests: "export contains the subject's expected data and no
// other user's data" + "password/token hashes, internal secrets... are
// absent" + "artifact is encrypted at rest... wrong KEK fails."
func TestPrivacyExport_FullLifecycle_ContainsOwnDataOnlyAndNoSecrets(t *testing.T) {
	m, _, store, ring := setupExportModule(t)
	ctx := context.Background()

	const email, password, fullName = "dana@example.test", "hunter22!super", "Dana Danaher"
	userID := registerTestUserNamed(t, m, email, password, fullName)
	otherUserID := registerTestUser(t, m, "erin@example.test", "hunter22!")
	_ = otherUserID

	req, err := m.RequestExport(ctx, userID, password)
	require.NoError(t, err)
	require.Equal(t, "pending", req.Status)

	require.NoError(t, m.AssembleOnePendingExport(ctx))

	got, err := m.GetExportStatus(ctx, userID, req.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", got.Status)
	require.NotNil(t, got.ExpiresAt)
	require.Greater(t, got.RowCount, 0)

	require.Equal(t, 1, len(store.objects), "exactly one archive object must exist (the other registered user must never trigger their own export)")
	var encrypted []byte
	for _, v := range store.objects {
		encrypted = v
	}
	require.NotContains(t, string(encrypted), email, "the archive must be encrypted at rest — the subject's own email must not appear in plaintext in the stored bytes")

	// "wrong KEK fails" — a differently-keyed ring must never decrypt this
	// archive.
	wrongRing := ringWithDifferentKey(t)
	_, openErr := wrongRing.Open(cryptox.AAD{Service: "auth", Table: "privacy_requests", Column: "object", RowID: req.ID.String()}, encrypted)
	require.Error(t, openErr, "a wrong KEK must fail to open the archive")

	plaintext, err := ring.Open(cryptox.AAD{Service: "auth", Table: "privacy_requests", Column: "object", RowID: req.ID.String()}, encrypted)
	require.NoError(t, err)

	zr, err := zip.NewReader(strings.NewReader(string(plaintext)), int64(len(plaintext)))
	require.NoError(t, err)
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		require.NoError(t, err)
		content, err := io.ReadAll(rc)
		require.NoError(t, err)
		rc.Close()
		files[f.Name] = content
	}
	require.Contains(t, files, "manifest.json")
	require.Contains(t, files, "auth.ndjson")

	ndjson := string(files["auth.ndjson"])
	require.Contains(t, ndjson, email, "the archive must contain the SUBJECT's own email")
	require.Contains(t, ndjson, fullName)
	require.NotContains(t, ndjson, "erin@example.test", "the archive must never contain another user's data")
	require.NotContains(t, ndjson, password, "the archive must never contain the raw password")
	require.NotContains(t, ndjson, "$2a$", "the archive must never contain a bcrypt hash prefix")
	require.NotContains(t, ndjson, "decided_by")
	require.NotContains(t, ndjson, "provider_ref")

	manifest := string(files["manifest.json"])
	require.Contains(t, manifest, `"schema_version"`)
	require.Contains(t, manifest, `"exclusions"`)
}

// TestPrivacyExport_Download_OneTimeAndEnqueuesCleanup is T4's own
// required test: "successful download... removes the object idempotently."
func TestPrivacyExport_Download_OneTimeAndEnqueuesCleanup(t *testing.T) {
	m, db, store, _ := setupExportModule(t)
	ctx := context.Background()
	const password = "hunter22!"
	userID := registerTestUser(t, m, "frank@example.test", password)

	req, err := m.RequestExport(ctx, userID, password)
	require.NoError(t, err)
	require.NoError(t, m.AssembleOnePendingExport(ctx))

	content, err := m.DownloadExport(ctx, userID, req.ID, password)
	require.NoError(t, err)
	require.NotEmpty(t, content)

	// Second download attempt must be refused — K9's own "one-time
	// streaming download."
	_, err = m.DownloadExport(ctx, userID, req.ID, password)
	require.ErrorIs(t, err, auth.ErrExportAlreadyDownloaded)

	// The object-delete outbox must have a pending entry for this
	// export's object key.
	worker, err := objectoutbox.NewWorker("auth", db, store, []objectoutbox.Target{
		{RefTable: "privacy_requests", MetadataUpdateSQL: `UPDATE privacy_requests SET updated_at = now() WHERE id = $1`},
	})
	require.NoError(t, err)
	processed, failed, err := worker.ProcessOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, failed)
	require.Equal(t, 1, processed)
	require.Empty(t, store.objects, "the archive object must actually be gone from the store after the outbox drains")
}

// TestPrivacyExport_TTLExpiry_EnqueuesCleanupIdempotently is T4's own
// required test: "TTL expiry... removes the object idempotently."
func TestPrivacyExport_TTLExpiry_EnqueuesCleanupIdempotently(t *testing.T) {
	m, db, store, _ := setupExportModule(t)
	ctx := context.Background()
	const password = "hunter22!"
	userID := registerTestUser(t, m, "grace@example.test", password)

	req, err := m.RequestExport(ctx, userID, password)
	require.NoError(t, err)
	require.NoError(t, m.AssembleOnePendingExport(ctx))
	require.Equal(t, 1, len(store.objects))

	// Simulate the 24h TTL having already elapsed.
	_, err = db.ExecContext(ctx, `UPDATE privacy_requests SET expires_at = $1 WHERE id = $2`, time.Now().Add(-time.Minute), req.ID)
	require.NoError(t, err)

	require.NoError(t, m.ExpireOneStaleExport(ctx))
	// Calling it again must be a safe no-op — nothing left eligible.
	require.NoError(t, m.ExpireOneStaleExport(ctx))

	got, err := m.GetExportStatus(ctx, userID, req.ID)
	require.NoError(t, err)
	require.Equal(t, "expired", got.Status)

	worker, err := objectoutbox.NewWorker("auth", db, store, []objectoutbox.Target{
		{RefTable: "privacy_requests", MetadataUpdateSQL: `UPDATE privacy_requests SET updated_at = now() WHERE id = $1`},
	})
	require.NoError(t, err)
	processed, failed, err := worker.ProcessOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, failed)
	require.Equal(t, 1, processed)
	require.Empty(t, store.objects)
}

// TestPrivacyExport_FailedAssembly_NeverProducesFalselyReadyRequest is
// T4's own required test: "a failed owner never produces a falsely
// complete manifest."
func TestPrivacyExport_FailedAssembly_NeverProducesFalselyReadyRequest(t *testing.T) {
	m, db, store, _ := setupExportModule(t)
	ctx := context.Background()
	const password = "hunter22!"
	userID := registerTestUser(t, m, "henry@example.test", password)

	req, err := m.RequestExport(ctx, userID, password)
	require.NoError(t, err)

	// Sabotage the row AFTER creation so collectAuthOwnerRows's own
	// cutoff-vs-created_at check fails deterministically — created_at
	// pushed after cutoff makes "user not found as of cutoff" fire.
	_, err = db.ExecContext(ctx, `UPDATE privacy_requests SET cutoff = $1 WHERE id = $2`, time.Now().Add(-24*time.Hour), req.ID)
	require.NoError(t, err)

	require.NoError(t, m.AssembleOnePendingExport(ctx))

	got, err := m.GetExportStatus(ctx, userID, req.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", got.Status, "an owner-collection failure must mark the request failed, never ready")
	require.NotEmpty(t, got.ErrorMessage)
	require.Empty(t, store.objects, "nothing must ever be uploaded for a failed assembly")
}

func ringWithDifferentKey(t *testing.T) *cryptox.Ring {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 200)
	}
	ring, err := cryptox.NewRing(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	return ring
}

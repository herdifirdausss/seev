package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/pkg/database"
)

// TestStartObjectOutboxWorker_NoDocumentStore_IsANoop proves
// StartObjectOutboxWorker returns (nil, nil) when no DocumentStore is
// configured — the real state of every binary in this repo today, since
// SetDocumentStore is never called anywhere in-tree (documents.go's own
// comment: storage is deliberately an interface with no default MinIO
// wiring). cmd/auth-service/main.go depends on this: it calls
// StartObjectOutboxWorker unconditionally and only guards stopObjectOutbox
// being nil at shutdown, never at call time.
func TestStartObjectOutboxWorker_NoDocumentStore_IsANoop(t *testing.T) {
	ctrl := gomock.NewController(t)
	m, _, _, _ := newTestModule(ctrl, &stubProvisioner{})

	stop, err := m.StartObjectOutboxWorker(context.Background(), discardLogger())
	require.NoError(t, err)
	assert.Nil(t, stop)
}

type fakeDocumentStore struct{}

func (fakeDocumentStore) Put(context.Context, string, []byte, string) error { return nil }
func (fakeDocumentStore) Get(context.Context, string) ([]byte, error)       { return nil, nil }
func (fakeDocumentStore) Delete(context.Context, string) error              { return nil }

// TestStartObjectOutboxWorker_WithDocumentStore_StartsAndStops proves the
// worker actually gets constructed and started once a DocumentStore is
// configured — the wiring cmd/auth-service/main.go depends on, exercised
// here without a real Postgres connection since the worker never queries
// until its first poll tick.
func TestStartObjectOutboxWorker_WithDocumentStore_StartsAndStops(t *testing.T) {
	ctrl := gomock.NewController(t)
	m, _, _, _ := newTestModule(ctrl, &stubProvisioner{})
	m.db = &database.MockDatabaseSQL{}
	m.documentStore = fakeDocumentStore{}

	stop, err := m.StartObjectOutboxWorker(context.Background(), discardLogger())
	require.NoError(t, err)
	require.NotNil(t, stop)
	stop()
}

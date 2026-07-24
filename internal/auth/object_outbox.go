package auth

import (
	"context"
	"log/slog"
	"time"

	"github.com/herdifirdausss/seev/pkg/objectoutbox"
)

// defaultObjectOutboxPollInterval matches internal/ledger/worker's own
// outbox relay poll cadence order of magnitude — object deletes are not
// latency-sensitive, but a KYC document should not sit in 'pending' for a
// full day just because this poll interval is generous.
const defaultObjectOutboxPollInterval = 30 * time.Second

// StartObjectOutboxWorker wires and starts auth's docs/roadmap/active/51-a8-data-lifecycle-privacy.md
// T1.6 (K6) object-delete outbox: draining auth_object_delete_outbox
// against m.documentStore (set via SetDocumentStore) and marking
// kyc_documents.deleted_at once the store confirms the object is gone.
// Returns (nil, nil) when no document store is configured — matches
// UploadKYCDocument/DownloadKYCDocument's own "storage is optional in this
// binary" convention (docs/roadmap/archive KYC storage is deliberately an
// interface with no default MinIO wiring yet).
func (m *Module) StartObjectOutboxWorker(ctx context.Context, logger *slog.Logger) (stop func(), err error) {
	if m.documentStore == nil {
		return nil, nil
	}
	worker, err := objectoutbox.NewWorker("auth", m.db, m.documentStore, []objectoutbox.Target{
		{RefTable: "kyc_documents", MetadataUpdateSQL: `UPDATE kyc_documents SET deleted_at = now() WHERE id = $1`},
		// docs/roadmap/active/51 T4 (K9): drains export-archive deletes enqueued by
		// both DownloadExport (successful one-time download) and
		// expireOneStaleExport (24h TTL) — object_key is already cleared
		// to NULL-equivalent by neither path; the row itself keeps its
		// object_key as a historical record, only the STORED OBJECT is
		// removed, so this metadata update is a no-op status touch, not a
		// column clear (unlike kyc_documents.deleted_at above, which has
		// no other field recording "this was deleted").
		{RefTable: "privacy_requests", MetadataUpdateSQL: `UPDATE privacy_requests SET updated_at = now() WHERE id = $1`},
	}, objectoutbox.WithLogger(logger))
	if err != nil {
		return nil, err
	}
	return worker.Start(ctx, defaultObjectOutboxPollInterval), nil
}

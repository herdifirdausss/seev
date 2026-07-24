package auth

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/auth/repository"
	"github.com/herdifirdausss/seev/pkg/objectoutbox"
)

const (
	exportPollInterval = 15 * time.Second
	// exportTTL is K9's own "an undownloaded export expires after 24
	// hours."
	exportTTL = 24 * time.Hour
	// exportSchemaVersion is the manifest's own schema_version — bump this
	// whenever a row shape below changes so an operator/tool reading an
	// old archive can tell the two apart.
	exportSchemaVersion = 1
	// exportRetentionPolicyVersion mirrors config/data-retention.yaml's own
	// PolicyVersion field convention — recorded in the manifest per K9's
	// own "retention policy version" requirement, bumped in lockstep with
	// that file whenever it changes.
	exportRetentionPolicyVersion = 1
)

// exportUserProfileRow and exportKYCSubmissionRow are docs/roadmap/active/51-a8-data-lifecycle-privacy.md
// T4's (work item 3) deterministic, explicitly-versioned export DTOs —
// hand-written, never a struct reused from another layer, so a future
// unrelated field added to model.User/model.KYCSubmission can never leak
// into an export archive by accident. KYC's raw `payload` (the actual
// submitted document data) and every internal/operator-only field
// (`decided_by`, `provider_ref`) are deliberately excluded — recorded in
// the manifest's own `exclusions` list, not just this comment.
type exportUserProfileRow struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	KYCLevel  int       `json:"kyc_level"`
	CreatedAt time.Time `json:"created_at"`
}

type exportKYCSubmissionRow struct {
	Type           string     `json:"type"`
	ID             string     `json:"id"`
	LevelRequested int        `json:"level_requested"`
	Status         string     `json:"status"`
	Provider       string     `json:"provider"`
	DecisionReason string     `json:"decision_reason,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
}

type exportManifestOwner struct {
	Owner    string `json:"owner"`
	RowCount int    `json:"row_count"`
	SHA256   string `json:"sha256"`
}

type exportManifest struct {
	SchemaVersion          int                   `json:"schema_version"`
	RequestID              string                `json:"request_id"`
	UserID                 string                `json:"user_id"`
	GeneratedAt            time.Time             `json:"generated_at"`
	Cutoff                 time.Time             `json:"cutoff"`
	RetentionPolicyVersion int                   `json:"retention_policy_version"`
	Owners                 []exportManifestOwner `json:"owners"`
	// Exclusions is K9's own "the manifest records... exclusions" —
	// machine-readable AND human-readable, so a caller inspecting the
	// archive never has to guess whether an absent field was omitted
	// deliberately or lost to a bug.
	Exclusions []string `json:"exclusions"`
}

var exportExclusions = []string{
	"password hashes and refresh/access tokens are never included",
	"raw KYC submission payload and uploaded document bytes are never included (auth.kyc_submission rows carry only the decision outcome)",
	"internal operator identities (decided_by) and vendor correlation ids (provider_ref) are never included",
	"payin: raw vendor webhook payloads (encrypted and plaintext) and error messages are never included",
	"payout: destination account details (encrypted and plaintext), vendor references, and error messages are never included",
	"gateway: internal notification rendering payload is never included (title/body already carry the user-facing content)",
	"ledger: only transaction headers are included, never raw ledger_entries (immutable financial evidence)",
	"admin-bff and assurance own no end-user data — never present in an export (K11)",
}

// StartPrivacyExportWorker wires and starts docs/roadmap/active/51-a8-data-lifecycle-privacy.md T4's
// (K9) export assembly + TTL-expiry sweep. Returns (nil, nil) when no
// document store or export ring is configured — matches
// StartObjectOutboxWorker's own "storage is optional in this binary"
// convention.
func (m *Module) StartPrivacyExportWorker(ctx context.Context, logger *slog.Logger) (stop func(), err error) {
	if m.documentStore == nil || m.exportRing == nil {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(exportPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := m.AssembleOnePendingExport(ctx); err != nil {
					logger.Error("privacy export: assemble failed", "error", err)
				}
				if err := m.ExpireOneStaleExport(ctx); err != nil {
					logger.Error("privacy export: expire sweep failed", "error", err)
				}
				m.refreshPrivacyRequestsGauge(ctx, logger)
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}

// AssembleOnePendingExport claims exactly one 'pending' request (FOR
// UPDATE SKIP LOCKED — safe under concurrent replicas), transitions it to
// 'collecting', builds the archive, and marks it 'ready' or 'failed'.
// Modeled on internal/payout/relay.go's own dispatchOne discipline: every
// exit path is accounted for — a request is NEVER left silently
// 'collecting' if this function returns, matching K9's own "a failed
// owner never produces a falsely complete manifest." Exported (like
// pkg/objectoutbox.Worker.ProcessOnce) so integration tests can drive it
// deterministically instead of racing the background poller.
func (m *Module) AssembleOnePendingExport(ctx context.Context) error {
	var id, userID uuid.UUID
	var cutoff, requestedAt time.Time
	claimed := false
	err := m.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `
			SELECT id, user_id, cutoff, requested_at FROM privacy_requests
			WHERE status = 'pending'
			ORDER BY requested_at
			LIMIT 1
			FOR UPDATE SKIP LOCKED`).Scan(&id, &userID, &cutoff, &requestedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE privacy_requests SET status = 'collecting', updated_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if err != nil || !claimed {
		return err
	}

	objectKey, manifestHash, rowCount, buildErr := m.buildAndUploadExport(ctx, id, userID, cutoff)
	if buildErr != nil {
		_, updErr := m.db.ExecContext(ctx, `
			UPDATE privacy_requests SET status = 'failed', error_message = $1, updated_at = now() WHERE id = $2`,
			truncateErrorMessage(buildErr.Error()), id)
		if updErr != nil {
			return fmt.Errorf("mark export failed after build error %v: %w", buildErr, updErr)
		}
		observePrivacyRequestDuration("export", "failed", requestedAt)
		return nil
	}

	readyAt := time.Now().UTC()
	expiresAt := readyAt.Add(exportTTL)
	_, err = m.db.ExecContext(ctx, `
		UPDATE privacy_requests
		SET status = 'ready', object_key = $1, manifest_hash = $2, row_count = $3, ready_at = $4, expires_at = $5, updated_at = now()
		WHERE id = $6`,
		objectKey, manifestHash, rowCount, readyAt, expiresAt, id)
	if err != nil {
		return fmt.Errorf("mark export ready: %w", err)
	}
	observePrivacyRequestDuration("export", "ready", requestedAt)
	return nil
}

// observePrivacyRequestDuration records K13's seev_privacy_request_duration_seconds
// at every terminal transition — kind is "export"|"closure", result is the
// terminal status just reached.
func observePrivacyRequestDuration(kind, result string, requestedAt time.Time) {
	privacyRequestDuration.WithLabelValues(kind, result).Observe(time.Since(requestedAt).Seconds())
}

// refreshPrivacyRequestsGauge updates seev_privacy_requests{kind,status}
// (K13) — same "refreshed once per worker tick" convention as
// pkg/retentionworker's own holdsGauge.
func (m *Module) refreshPrivacyRequestsGauge(ctx context.Context, logger *slog.Logger) {
	rows, err := m.db.QueryContext(ctx, `SELECT request_type, status, count(*) FROM privacy_requests GROUP BY request_type, status`)
	if err != nil {
		logger.Error("privacy: refresh requests gauge failed", "error", err)
		return
	}
	defer rows.Close()
	privacyRequestsGauge.Reset()
	for rows.Next() {
		var kind, status string
		var count int
		if err := rows.Scan(&kind, &status, &count); err != nil {
			logger.Error("privacy: scan requests gauge row failed", "error", err)
			return
		}
		privacyRequestsGauge.WithLabelValues(kind, status).Set(float64(count))
	}
	if err := rows.Err(); err != nil {
		logger.Error("privacy: iterate requests gauge rows failed", "error", err)
	}
}

// buildAndUploadExport collects auth's own owner data plus every
// REGISTERED owner's export data (A8 T4b: ledger, payin, payout, fraud,
// gateway — reuses the exact same client registry `m.closureOwners` the
// closure saga registers via RegisterClosureOwner, since every owner's
// export and closure endpoints live behind the identical client/base
// URL; admin-bff and assurance are never registered — K11's own "no
// end-user data"/"no hidden subject field" findings mean neither has
// anything to export), assembles manifest.json + one NDJSON file per
// owner into a ZIP, encrypts the whole archive under the dedicated export
// ring, and uploads it.
func (m *Module) buildAndUploadExport(ctx context.Context, requestID, userID uuid.UUID, cutoff time.Time) (objectKey, manifestHash string, rowCount int, err error) {
	authRows, err := m.collectAuthOwnerRows(ctx, userID, cutoff)
	if err != nil {
		return "", "", 0, fmt.Errorf("collect auth owner data: %w", err)
	}
	authNDJSON, err := toNDJSON(authRows)
	if err != nil {
		return "", "", 0, fmt.Errorf("encode auth owner ndjson: %w", err)
	}
	authHash := sha256.Sum256(authNDJSON)

	manifest := exportManifest{
		SchemaVersion: exportSchemaVersion, RequestID: requestID.String(), UserID: userID.String(),
		GeneratedAt: time.Now().UTC(), Cutoff: cutoff, RetentionPolicyVersion: exportRetentionPolicyVersion,
		Owners:     []exportManifestOwner{{Owner: "auth", RowCount: len(authRows), SHA256: hex.EncodeToString(authHash[:])}},
		Exclusions: exportExclusions,
	}

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	if err := writeZipFile(zw, "auth.ndjson", authNDJSON); err != nil {
		return "", "", 0, err
	}
	totalRows := len(authRows)

	for _, owner := range m.closureOwners {
		ownerRows, err := owner.client.Export(ctx, userID, cutoff)
		if err != nil {
			return "", "", 0, fmt.Errorf("collect %s owner data: %w", owner.name, err)
		}
		ownerNDJSON := rawRowsToNDJSON(ownerRows)
		ownerHash := sha256.Sum256(ownerNDJSON)
		manifest.Owners = append(manifest.Owners, exportManifestOwner{
			Owner: owner.name, RowCount: len(ownerRows), SHA256: hex.EncodeToString(ownerHash[:]),
		})
		if err := writeZipFile(zw, owner.name+".ndjson", ownerNDJSON); err != nil {
			return "", "", 0, err
		}
		totalRows += len(ownerRows)
	}

	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", "", 0, fmt.Errorf("encode manifest: %w", err)
	}
	manifestSum := sha256.Sum256(manifestJSON)
	if err := writeZipFile(zw, "manifest.json", manifestJSON); err != nil {
		return "", "", 0, err
	}
	if err := zw.Close(); err != nil {
		return "", "", 0, fmt.Errorf("close zip writer: %w", err)
	}

	encrypted, err := m.exportRing.Seal(exportAAD(requestID), zipBuf.Bytes())
	if err != nil {
		return "", "", 0, fmt.Errorf("encrypt export archive: %w", err)
	}
	// Opaque object key (K2's own "no user UUID in the object path"
	// principle, same as KYC documents) — the user/export relationship
	// lives only in the encrypted privacy_requests row, never derivable
	// from the object store's own path.
	key := "exports/" + requestID.String()
	if err := m.documentStore.Put(ctx, key, encrypted, "application/octet-stream"); err != nil {
		return "", "", 0, fmt.Errorf("upload export archive: %w", err)
	}
	return key, hex.EncodeToString(manifestSum[:]), totalRows, nil
}

// rawRowsToNDJSON joins already-JSON-encoded owner export rows with
// newlines — unlike toNDJSON below (which marshals Go values), owner
// rows arrive pre-marshaled from the wire (internal/auth.OwnerClosureClient.Export).
func rawRowsToNDJSON(rows []json.RawMessage) []byte {
	var buf bytes.Buffer
	for _, row := range rows {
		buf.Write(row)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// collectAuthOwnerRows reads the user profile through UserRepository (the
// only path that can decrypt auth_users.email/full_name since "A8 T2.5b"'s
// contract migration dropped their plaintext columns) and every other
// auth table directly (bypassing KYCRepository for the KYC decision
// summary — read-only, same-database, cross-cutting concern, the same
// convention internal/auth/retention.go and object_outbox.go already use;
// KYC's own encrypted payload is never selected here regardless, see the
// exclusions list) — ordered deterministically (created_at, id) per work
// item 3's own "stable ordering" requirement.
func (m *Module) collectAuthOwnerRows(ctx context.Context, userID uuid.UUID, cutoff time.Time) ([]any, error) {
	var rows []any

	u, err := m.users.GetUserByID(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, fmt.Errorf("user %s not found", userID)
	}
	if err != nil {
		return nil, err
	}
	if u.CreatedAt.After(cutoff) {
		return nil, fmt.Errorf("user %s not found as of cutoff", userID)
	}
	rows = append(rows, exportUserProfileRow{
		Type: "user_profile", ID: u.ID.String(), Email: u.Email, FullName: u.FullName,
		Role: u.Role, Status: u.Status, KYCLevel: u.KYCLevel, CreatedAt: u.CreatedAt,
	})

	subRows, err := m.db.QueryContext(ctx, `
		SELECT id, level_requested, status, provider, COALESCE(decision_reason,''), created_at, decided_at
		FROM kyc_submissions WHERE user_id = $1 AND created_at <= $2
		ORDER BY created_at, id`, userID, cutoff)
	if err != nil {
		return nil, err
	}
	defer subRows.Close()
	for subRows.Next() {
		var id uuid.UUID
		var levelRequested int
		var status, provider, reason string
		var createdAt time.Time
		var decidedAt sql.NullTime
		if err := subRows.Scan(&id, &levelRequested, &status, &provider, &reason, &createdAt, &decidedAt); err != nil {
			return nil, err
		}
		row := exportKYCSubmissionRow{
			Type: "kyc_submission", ID: id.String(), LevelRequested: levelRequested,
			Status: status, Provider: provider, DecisionReason: reason, CreatedAt: createdAt,
		}
		if decidedAt.Valid {
			t := decidedAt.Time
			row.DecidedAt = &t
		}
		rows = append(rows, row)
	}
	if err := subRows.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

// ExpireOneStaleExport claims exactly one 'ready', undownloaded,
// past-expiry request and enqueues its object for deletion — K9's own
// "successful download and TTL expiry each remove the object
// idempotently": objectoutbox.Enqueue's ON CONFLICT DO NOTHING means this
// is always safe to run even if a download raced it and already enqueued
// the same object. Exported for the same deterministic-testing reason as
// AssembleOnePendingExport above.
func (m *Module) ExpireOneStaleExport(ctx context.Context) error {
	var id uuid.UUID
	var objectKey string
	var requestedAt time.Time
	claimed := false
	err := m.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `
			SELECT id, object_key, requested_at FROM privacy_requests
			WHERE status = 'ready' AND downloaded_at IS NULL AND expires_at < now()
			ORDER BY expires_at
			LIMIT 1
			FOR UPDATE SKIP LOCKED`).Scan(&id, &objectKey, &requestedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE privacy_requests SET status = 'expired', updated_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		if err := objectoutbox.Enqueue(ctx, tx, "auth", "privacy_requests", id, objectKey); err != nil {
			privacyObjectDeleteTotal.WithLabelValues("export", "enqueue_failed").Inc()
			return err
		}
		claimed = true
		return nil
	})
	if err != nil || !claimed {
		return err
	}
	privacyObjectDeleteTotal.WithLabelValues("export", "enqueued").Inc()
	observePrivacyRequestDuration("export", "expired", requestedAt)
	return nil
}

func writeZipFile(zw *zip.Writer, name string, content []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("create zip entry %s: %w", name, err)
	}
	if _, err := w.Write(content); err != nil {
		return fmt.Errorf("write zip entry %s: %w", name, err)
	}
	return nil
}

func toNDJSON(rows []any) ([]byte, error) {
	var buf bytes.Buffer
	for _, row := range rows {
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		buf.Write(encoded)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func truncateErrorMessage(s string) string {
	const max = 500
	if len(s) > max {
		return s[:max]
	}
	return s
}

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/pkg/cryptox"
)

var (
	ErrDocumentStorageUnavailable = errors.New("auth: document storage unavailable")
	ErrDocumentInvalid            = errors.New("auth: invalid KYC document")
)

type DocumentStore interface {
	Put(context.Context, string, []byte, string) error
	Get(context.Context, string) ([]byte, error)
	// Delete must be idempotent: deleting an already-absent key is
	// success, not an error (docs/roadmap/archive/51-a8-data-lifecycle-privacy.md K6 —
	// pkg/objectoutbox.Worker relies on this for safe retry after a
	// partial failure).
	Delete(context.Context, string) error
}

// Module's storage is deliberately an interface: the default binary has no
// MinIO dependency or credentials, while production composition can provide a
// hardened object-store adapter.
func (m *Module) SetDocumentStore(store DocumentStore) { m.documentStore = store }

// SetDocumentKeyRing wires the pkg/cryptox.Ring KYC document encryption
// uses (docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T2.2 — cmd/auth-service/main.go
// constructs this from cfg.Cryptox.Ring(), backed by CRYPTOX_KEY_V1_FILE/
// CRYPTOX_KEY_CURRENT_VERSION). A nil ring disables document encryption
// (UploadKYCDocument/DownloadKYCDocument return ErrDocumentStorageUnavailable),
// matching every prior version of this same optionality.
func (m *Module) SetDocumentKeyRing(ring *cryptox.Ring) { m.documentRing = ring }

// documentAAD binds a KYC document's ciphertext to this specific document
// row — copying an encrypted blob into a different document's object-store
// slot changes RowID, so pkg/cryptox.Ring.Open fails closed (K2).
func documentAAD(documentID uuid.UUID) cryptox.AAD {
	return cryptox.AAD{Service: "auth", Table: "kyc_documents", Column: "object", RowID: documentID.String()}
}

func (m *Module) UploadKYCDocument(ctx context.Context, userID uuid.UUID, contentType string, plaintext []byte) (model.KYCDocument, error) {
	if m.documentStore == nil || m.documentRing == nil {
		return model.KYCDocument{}, ErrDocumentStorageUnavailable
	}
	if len(plaintext) == 0 || len(plaintext) > 10<<20 {
		return model.KYCDocument{}, fmt.Errorf("%w: size must be between 1 and 10 MiB", ErrDocumentInvalid)
	}
	allowed := map[string]bool{"application/pdf": true, "image/jpeg": true, "image/png": true}
	if !allowed[contentType] {
		return model.KYCDocument{}, fmt.Errorf("%w: MIME type is not allowed", ErrDocumentInvalid)
	}
	submission, err := m.kyc.GetLatestKYCSubmission(ctx, userID)
	if err != nil {
		return model.KYCDocument{}, err
	}

	documentID := uuid.New()
	sum := sha256.Sum256(plaintext)
	encrypted, err := m.documentRing.Seal(documentAAD(documentID), plaintext)
	if err != nil {
		return model.KYCDocument{}, fmt.Errorf("%w: %w", ErrDocumentInvalid, err)
	}
	// docs/roadmap/active/51 K2: "opaque random path with no user UUID" — the
	// document/user relationship lives only in the encrypted kyc_documents
	// row, never derivable from the object store's own path. documentID is
	// already a fresh, unique random UUID minted above; reusing it here
	// means no second random value or extra column is needed to satisfy
	// this.
	document := model.KYCDocument{ID: documentID, SubmissionID: submission.ID, UserID: userID, ObjectKey: "kyc/" + documentID.String(), SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(plaintext)), ContentType: contentType, CreatedAt: time.Now()}
	if err := m.documentStore.Put(ctx, document.ObjectKey, encrypted, "application/octet-stream"); err != nil {
		return model.KYCDocument{}, ErrDocumentStorageUnavailable
	}
	if err := m.kyc.CreateKYCDocument(ctx, document); err != nil {
		return model.KYCDocument{}, err
	}
	return document, nil
}

func (m *Module) DownloadKYCDocument(ctx context.Context, id uuid.UUID) (model.KYCDocument, []byte, error) {
	if m.documentStore == nil || m.documentRing == nil {
		return model.KYCDocument{}, nil, ErrDocumentStorageUnavailable
	}
	document, err := m.kyc.GetKYCDocument(ctx, id)
	if err != nil {
		return model.KYCDocument{}, nil, err
	}
	encrypted, err := m.documentStore.Get(ctx, document.ObjectKey)
	if err != nil {
		return model.KYCDocument{}, nil, ErrDocumentStorageUnavailable
	}
	plaintext, err := m.documentRing.Open(documentAAD(document.ID), encrypted)
	if err != nil {
		return model.KYCDocument{}, nil, fmt.Errorf("%w: %w", ErrDocumentInvalid, err)
	}
	return document, plaintext, nil
}

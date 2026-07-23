package auth

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/auth/model"
)

var (
	ErrDocumentStorageUnavailable = errors.New("auth: document storage unavailable")
	ErrDocumentInvalid            = errors.New("auth: invalid KYC document")
)

type DocumentStore interface {
	Put(context.Context, string, []byte, string) error
	Get(context.Context, string) ([]byte, error)
}

// Module's storage is deliberately an interface: the default binary has no
// MinIO dependency or credentials, while production composition can provide a
// hardened object-store adapter.
func (m *Module) SetDocumentStore(store DocumentStore) { m.documentStore = store }

func (m *Module) SetDocumentKEK(kek []byte) {
	if len(kek) == 0 {
		m.documentKEK = nil
		return
	}
	m.documentKEK = append([]byte(nil), kek...)
}

func EncryptDocument(kek, plaintext []byte) ([]byte, string, error) {
	if len(kek) != 32 {
		return nil, "", fmt.Errorf("%w: KEK must be 32 bytes", ErrDocumentInvalid)
	}
	if len(plaintext) == 0 {
		return nil, "", fmt.Errorf("%w: empty document", ErrDocumentInvalid)
	}
	dek := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, "", fmt.Errorf("generate document key: %w", err)
	}
	dataCipher, err := aesGCMEncrypt(dek, plaintext)
	if err != nil {
		return nil, "", err
	}
	wrapped, err := aesGCMEncrypt(kek, dek)
	if err != nil {
		return nil, "", err
	}
	// version | wrapped length | wrapped DEK | ciphertext. The DEK and
	// plaintext are never persisted separately or logged.
	out := bytes.NewBuffer(make([]byte, 0, 5+len(wrapped)+len(dataCipher)))
	out.WriteString("KYC1")
	_ = binary.Write(out, binary.BigEndian, uint32(len(wrapped)))
	out.Write(wrapped)
	out.Write(dataCipher)
	sum := sha256.Sum256(plaintext)
	return out.Bytes(), hex.EncodeToString(sum[:]), nil
}

func DecryptDocument(kek, encrypted []byte) ([]byte, error) {
	if len(kek) != 32 || len(encrypted) < 8 || string(encrypted[:4]) != "KYC1" {
		return nil, fmt.Errorf("%w: invalid envelope", ErrDocumentInvalid)
	}
	wrappedLen := binary.BigEndian.Uint32(encrypted[4:8])
	if wrappedLen < 12 || int(8+wrappedLen) >= len(encrypted) {
		return nil, fmt.Errorf("%w: invalid envelope lengths", ErrDocumentInvalid)
	}
	dek, err := aesGCMDecrypt(kek, encrypted[8:8+wrappedLen])
	if err != nil {
		return nil, fmt.Errorf("%w: unwrap key", ErrDocumentInvalid)
	}
	return aesGCMDecrypt(dek, encrypted[8+wrappedLen:])
}

func aesGCMEncrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return append(nonce, gcm.Seal(nil, nonce, plaintext, nil)...), nil
}

func aesGCMDecrypt(key, encrypted []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(encrypted) < gcm.NonceSize() {
		return nil, errors.New("invalid encrypted document")
	}
	nonce, ciphertext := encrypted[:gcm.NonceSize()], encrypted[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func (m *Module) UploadKYCDocument(ctx context.Context, userID uuid.UUID, contentType string, plaintext []byte) (model.KYCDocument, error) {
	if m.documentStore == nil || len(m.documentKEK) != 32 {
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
	encrypted, sum, err := EncryptDocument(m.documentKEK, plaintext)
	if err != nil {
		return model.KYCDocument{}, err
	}
	document := model.KYCDocument{ID: uuid.New(), SubmissionID: submission.ID, UserID: userID, ObjectKey: "kyc/" + userID.String() + "/" + uuid.NewString(), SHA256: sum, SizeBytes: int64(len(plaintext)), ContentType: contentType, CreatedAt: time.Now()}
	if err := m.documentStore.Put(ctx, document.ObjectKey, encrypted, "application/octet-stream"); err != nil {
		return model.KYCDocument{}, ErrDocumentStorageUnavailable
	}
	if err := m.kyc.CreateKYCDocument(ctx, document); err != nil {
		return model.KYCDocument{}, err
	}
	return document, nil
}

func (m *Module) DownloadKYCDocument(ctx context.Context, id uuid.UUID) (model.KYCDocument, []byte, error) {
	if m.documentStore == nil || len(m.documentKEK) != 32 {
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
	plaintext, err := DecryptDocument(m.documentKEK, encrypted)
	if err != nil {
		return model.KYCDocument{}, nil, err
	}
	return document, plaintext, nil
}

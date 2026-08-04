package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/services/auth/internal/auth/model"
)

type fakeDocStore struct{ objects map[string][]byte }

func (f *fakeDocStore) Put(_ context.Context, key string, data []byte, _ string) error {
	if f.objects == nil {
		f.objects = map[string][]byte{}
	}
	f.objects[key] = append([]byte(nil), data...)
	return nil
}
func (f *fakeDocStore) Get(_ context.Context, key string) ([]byte, error) {
	data, ok := f.objects[key]
	if !ok {
		return nil, ErrDocumentStorageUnavailable
	}
	return data, nil
}
func (f *fakeDocStore) Delete(_ context.Context, key string) error {
	delete(f.objects, key)
	return nil
}

func bytes32(b byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = b
	}
	return out
}

func ringWithKey(t *testing.T, b byte) *cryptox.Ring {
	t.Helper()
	ring, err := cryptox.NewRing(map[int][]byte{1: bytes32(b)}, 1)
	require.NoError(t, err)
	return ring
}

// TestUploadDownloadKYCDocument_RoundTripThroughCryptox proves
// documents.go's own Seal/Open wiring through internal/platform/security/crypto end to end: the
// stored object is never the plaintext. internal/platform/security/crypto's own test suite
// covers wrong key/AAD/copied ciphertext/truncated envelope/rotation in
// isolation; TestDownloadKYCDocument_WrongKey_FailsClosed below exercises
// the "wrong key" case through this actual code path too.
func TestUploadDownloadKYCDocument_RoundTripThroughCryptox(t *testing.T) {
	ctrl := gomock.NewController(t)
	m, _, _, kycMock := newTestModule(ctrl, &stubProvisioner{})

	store := &fakeDocStore{}
	m.SetDocumentStore(store)
	m.SetDocumentKeyRing(ringWithKey(t, 7))

	userID := uuid.New()
	submissionID := uuid.New()
	kycMock.EXPECT().GetLatestKYCSubmission(gomock.Any(), userID).Return(model.KYCSubmission{ID: submissionID}, nil)
	kycMock.EXPECT().CreateKYCDocument(gomock.Any(), gomock.Any()).Return(nil)

	plaintext := []byte("identity document bytes")
	doc, err := m.UploadKYCDocument(context.Background(), userID, "application/pdf", plaintext)
	require.NoError(t, err)
	require.Len(t, doc.SHA256, 64)

	stored, ok := store.objects[doc.ObjectKey]
	require.True(t, ok)
	require.NotContains(t, string(stored), string(plaintext), "the object store must only ever hold ciphertext")

	kycMock.EXPECT().GetKYCDocument(gomock.Any(), doc.ID).Return(doc, nil)
	_, got, err := m.DownloadKYCDocument(context.Background(), doc.ID)
	require.NoError(t, err)
	require.Equal(t, plaintext, got)
}

// TestDownloadKYCDocument_WrongKey_FailsClosed is T2's own required test
// ("wrong key") exercised through the actual Upload/Download code path.
func TestDownloadKYCDocument_WrongKey_FailsClosed(t *testing.T) {
	ctrl := gomock.NewController(t)
	m, _, _, kycMock := newTestModule(ctrl, &stubProvisioner{})

	store := &fakeDocStore{}
	m.SetDocumentStore(store)
	m.SetDocumentKeyRing(ringWithKey(t, 7))

	userID := uuid.New()
	submissionID := uuid.New()
	kycMock.EXPECT().GetLatestKYCSubmission(gomock.Any(), userID).Return(model.KYCSubmission{ID: submissionID}, nil)
	kycMock.EXPECT().CreateKYCDocument(gomock.Any(), gomock.Any()).Return(nil)

	doc, err := m.UploadKYCDocument(context.Background(), userID, "application/pdf", []byte("identity document bytes"))
	require.NoError(t, err)

	// Simulate a process restart with a different key — same store, same
	// document row, wrong key.
	m.SetDocumentKeyRing(ringWithKey(t, 8))
	kycMock.EXPECT().GetKYCDocument(gomock.Any(), doc.ID).Return(doc, nil)
	_, _, err = m.DownloadKYCDocument(context.Background(), doc.ID)
	require.ErrorIs(t, err, ErrDocumentInvalid)
}

func TestSetDocumentKeyRing_NilDisablesEncryption(t *testing.T) {
	ctrl := gomock.NewController(t)
	m, _, _, _ := newTestModule(ctrl, &stubProvisioner{})
	m.SetDocumentStore(&fakeDocStore{})
	m.SetDocumentKeyRing(ringWithKey(t, 7))
	require.NotNil(t, m.documentRing)

	m.SetDocumentKeyRing(nil)
	_, err := m.UploadKYCDocument(context.Background(), uuid.New(), "application/pdf", []byte("x"))
	require.ErrorIs(t, err, ErrDocumentStorageUnavailable)
}

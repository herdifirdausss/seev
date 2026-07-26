package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileDocumentStore_RoundTripAndIdempotentDelete(t *testing.T) {
	store, err := NewFileDocumentStore(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, store.Put(ctx, "exports/request-id", []byte("ciphertext"), "application/octet-stream"))
	got, err := store.Get(ctx, "exports/request-id")
	require.NoError(t, err)
	require.Equal(t, []byte("ciphertext"), got)
	require.NoError(t, store.Delete(ctx, "exports/request-id"))
	require.NoError(t, store.Delete(ctx, "exports/request-id"))
}

func TestFileDocumentStore_RejectsTraversal(t *testing.T) {
	store, err := NewFileDocumentStore(t.TempDir())
	require.NoError(t, err)
	require.ErrorIs(t, store.Put(context.Background(), "../escape", []byte("x"), ""), ErrDocumentInvalid)
}

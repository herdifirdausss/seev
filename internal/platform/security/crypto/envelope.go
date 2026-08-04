// Package cryptox is the domain-neutral envelope-encryption primitive
// docs/roadmap/archive/51-a8-data-lifecycle-privacy.md K2 requires: extracted and hardened
// from services/auth/internal/auth/documents.go's original KYC-document-only AES-GCM
// envelope (that file's own EncryptDocument/DecryptDocument had no AAD at
// all — a ciphertext copied into a different row or column would still
// decrypt cleanly, which is exactly the gap K2 exists to close).
//
// Every value is protected by envelope encryption: a random, single-use
// AES-256 data key (DEK) encrypts the plaintext; the DEK itself is wrapped
// by a versioned key-encryption key (KEK) from a Ring. Both layers
// authenticate the same AAD (service/table/column/row ID/version) — an
// attacker who moves a ciphertext to a different row, column, or table
// changes the AAD presented at decrypt time, so Open fails closed before
// any plaintext is produced.
package cryptox

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ErrInvalidEnvelope covers every structural/authentication failure: wrong
// key, wrong AAD, truncated bytes, or a ciphertext moved to a different
// row/column/table. Deliberately one error for all of these — the whole
// point of authenticated encryption is that a caller must never be able to
// distinguish "wrong key" from "tampered ciphertext" from "moved to the
// wrong row" from the returned error alone.
var ErrInvalidEnvelope = errors.New("cryptox: invalid or unauthenticated envelope")

// magic identifies this package's own envelope format — distinct from the
// original "KYC1" prefix services/auth/internal/auth/documents.go used, since this is a
// different (AAD-bound) wire format, not a byte-compatible successor.
var magic = [4]byte{'C', 'R', 'X', '1'}

const keySize = 32 // AES-256

// dekEncrypt/dekDecrypt operate at the DEK layer (the actual plaintext).
// kekEncrypt/kekDecrypt operate at the KEK layer (wrapping the DEK). Both
// share the same AES-GCM primitive and both authenticate the same AAD —
// see the package doc comment for why binding it at both layers matters.

func gcmSeal(key, aad, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cryptox: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cryptox: new gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("cryptox: generate nonce: %w", err)
	}
	return append(nonce, gcm.Seal(nil, nonce, plaintext, aad)...), nil
}

func gcmOpen(key, aad, sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < gcm.NonceSize() {
		return nil, ErrInvalidEnvelope
	}
	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	return plaintext, nil
}

// seal encrypts plaintext under a fresh random DEK, wraps the DEK with kek
// (keyVersion identifies which KEK in the Ring it came from), and
// authenticates aad at both layers. Returns the serialized envelope:
// magic | keyVersion(4 BE) | len(wrappedDEK)(4 BE) | wrappedDEK | dataCiphertext.
func seal(kek []byte, keyVersion int, aad AAD, plaintext []byte) ([]byte, error) {
	if len(kek) != keySize {
		return nil, fmt.Errorf("cryptox: KEK must be %d bytes", keySize)
	}
	aadBytes := aad.bytes()

	dek := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, fmt.Errorf("cryptox: generate DEK: %w", err)
	}
	defer Zero(dek)

	dataCiphertext, err := gcmSeal(dek, aadBytes, plaintext)
	if err != nil {
		return nil, err
	}
	wrappedDEK, err := gcmSeal(kek, aadBytes, dek)
	if err != nil {
		return nil, err
	}

	out := bytes.NewBuffer(make([]byte, 0, 4+4+4+len(wrappedDEK)+len(dataCiphertext)))
	out.Write(magic[:])
	_ = binary.Write(out, binary.BigEndian, uint32(keyVersion)) //nolint:gosec // key versions are small, positive, operator-assigned integers.
	_ = binary.Write(out, binary.BigEndian, uint32(len(wrappedDEK)))
	out.Write(wrappedDEK)
	out.Write(dataCiphertext)
	return out.Bytes(), nil
}

// open reverses seal: unwraps the DEK with kek, then decrypts the data
// ciphertext, authenticating aad at both layers. keyVersion is read from
// the envelope's own header — the caller (Ring.Open) uses it to select
// which KEK to try, never trusts it for anything else.
func open(kek []byte, aad AAD, envelope []byte) ([]byte, error) {
	if len(kek) != keySize {
		return nil, ErrInvalidEnvelope
	}
	header, wrappedDEK, dataCiphertext, err := parseEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	_ = header

	aadBytes := aad.bytes()
	dek, err := gcmOpen(kek, aadBytes, wrappedDEK)
	if err != nil {
		return nil, err
	}
	defer Zero(dek)

	return gcmOpen(dek, aadBytes, dataCiphertext)
}

type envelopeHeader struct {
	keyVersion int
}

func parseEnvelope(envelope []byte) (header envelopeHeader, wrappedDEK, dataCiphertext []byte, err error) {
	if len(envelope) < 12 || !bytes.Equal(envelope[:4], magic[:]) {
		return envelopeHeader{}, nil, nil, ErrInvalidEnvelope
	}
	keyVersion := binary.BigEndian.Uint32(envelope[4:8])
	wrappedLen := binary.BigEndian.Uint32(envelope[8:12])
	rest := envelope[12:]
	if uint64(wrappedLen) > uint64(len(rest)) { //nolint:gosec // explicit bounds check, not a truncation.
		return envelopeHeader{}, nil, nil, ErrInvalidEnvelope
	}
	wrappedDEK = rest[:wrappedLen]
	dataCiphertext = rest[wrappedLen:]
	if len(dataCiphertext) == 0 {
		return envelopeHeader{}, nil, nil, ErrInvalidEnvelope
	}
	return envelopeHeader{keyVersion: int(keyVersion)}, wrappedDEK, dataCiphertext, nil
}

// EnvelopeKeyVersion reads only the key-version header from a serialized
// envelope without attempting to decrypt anything — Ring.Open uses this to
// pick which KEK to try first, and callers needing to report "which key
// version is this row still on" (e.g. a rotation-progress query) can use
// it directly without a KEK at all.
func EnvelopeKeyVersion(envelope []byte) (int, error) {
	header, _, _, err := parseEnvelope(envelope)
	if err != nil {
		return 0, err
	}
	return header.keyVersion, nil
}

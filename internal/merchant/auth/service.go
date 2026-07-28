package auth

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// ErrTooManyActiveKeys is returned by CreateKey when the tenant already
// has two active keys in the target environment (§8.4: "a tenant may
// have at most two active keys per environment").
var ErrTooManyActiveKeys = fmt.Errorf("merchant/auth: tenant already has the maximum active keys for this environment")

// ErrUnknownScope is returned by CreateKey for any scope not in
// AllScopes (§7.2).
var ErrUnknownScope = fmt.Errorf("merchant/auth: unknown scope")

// KeyService is T3's "operator create/rotate/revoke application
// services" — called by Admin BFF (T8), never directly by a merchant.
type KeyService struct {
	keys   repository.APIKeyRepository
	pepper string
}

func NewKeyService(keys repository.APIKeyRepository, pepper string) *KeyService {
	if pepper == "" {
		panic("merchant/auth: NewKeyService requires a non-empty pepper")
	}
	return &KeyService{keys: keys, pepper: pepper}
}

// CreateKey generates, digests, and stores a new key, returning its
// one-time plaintext (§8.1). It enforces the at-most-two-active-keys
// limit (§8.4) and rejects any unknown scope (§7.2) before the key is
// ever generated — an operator never gets a plaintext key back for a
// request that was going to be rejected anyway.
func (s *KeyService) CreateKey(ctx context.Context, tenantID uuid.UUID, environment string, scopes []string, createdBy string) (plaintext string, keyID uuid.UUID, err error) {
	for _, scope := range scopes {
		if !ValidScope(scope) {
			return "", uuid.Nil, fmt.Errorf("%w: %q", ErrUnknownScope, scope)
		}
	}

	existing, err := s.keys.ListByTenant(ctx, tenantID)
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("merchant/auth: create key: %w", err)
	}
	activeInEnv := 0
	for _, k := range existing {
		if k.Status == "active" && k.Environment == environment {
			activeInEnv++
		}
	}
	if activeInEnv >= 2 {
		return "", uuid.Nil, ErrTooManyActiveKeys
	}

	generated, err := GenerateKey(environment)
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("merchant/auth: create key: %w", err)
	}
	digest, err := Digest(s.pepper, generated.Plaintext)
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("merchant/auth: create key: %w", err)
	}

	id := generalutil.NewV7()
	publicID := "key_" + uuid.NewString()[:16]
	if err := s.keys.Create(ctx, model.APIKey{
		ID: id, PublicID: publicID, TenantID: tenantID, PublicPrefix: generated.PublicPrefix,
		SecretDigest: digest, Environment: environment, Status: "active", CreatedBy: createdBy, Scopes: scopes,
	}); err != nil {
		return "", uuid.Nil, fmt.Errorf("merchant/auth: create key: %w", err)
	}
	return generated.Plaintext, id, nil
}

// RotateKey creates a fresh key for the same tenant/environment/scopes,
// then revokes the old one — §8.4's "new and old keys may overlap for
// controlled rotation" means revocation happens SECOND, after the new key
// is confirmed created, so a failure between the two steps never leaves
// the tenant with zero working keys.
func (s *KeyService) RotateKey(ctx context.Context, tenantID, oldKeyID uuid.UUID, environment string, scopes []string, actor string) (plaintext string, newKeyID uuid.UUID, err error) {
	plaintext, newKeyID, err = s.CreateKey(ctx, tenantID, environment, scopes, actor)
	if err != nil {
		return "", uuid.Nil, err
	}
	if err := s.keys.Revoke(ctx, tenantID, oldKeyID, actor); err != nil {
		return plaintext, newKeyID, fmt.Errorf("merchant/auth: rotate key: new key %s created but revoking old key failed: %w", newKeyID, err)
	}
	return plaintext, newKeyID, nil
}

// RevokeKey immediately revokes a key (§8.4: "revocation takes effect
// immediately") — tenant-scoped per §7.3.
func (s *KeyService) RevokeKey(ctx context.Context, tenantID, keyID uuid.UUID, actor string) error {
	return s.keys.Revoke(ctx, tenantID, keyID, actor)
}

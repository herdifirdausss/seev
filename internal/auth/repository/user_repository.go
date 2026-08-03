package repository

//go:generate mockgen -source=user_repository.go -destination=user_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/pkg/cryptox"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalerror"
)

// UserRepository persists auth identities and credentials.
type UserRepository interface {
	// CreateUser inserts the identity + credential rows in one transaction.
	// Returns ErrDuplicateEmail on a case-insensitive email collision.
	CreateUser(ctx context.Context, u model.User, passwordHash string) error
	// GetUserByEmail looks up by the deterministic lookup digest.
	// ErrNotFound when absent.
	GetUserByEmail(ctx context.Context, email string) (model.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (model.User, error)
	// GetPasswordHash returns the bcrypt hash for the user — the ONLY read
	// path for auth_credentials, used solely inside Module.Login.
	GetPasswordHash(ctx context.Context, userID uuid.UUID) (string, error)
	UpdateFullName(ctx context.Context, userID uuid.UUID, fullName string) error
}

type userRepo struct {
	db     database.DatabaseSQL
	ring   *cryptox.Ring
	lookup *cryptox.LookupKey
}

// NewUserRepository's ring/lookup are both REQUIRED — docs/roadmap/archive/51-a8-data-lifecycle-privacy.md
// "A8 T2.5b" (the contract migration): auth_users.email/full_name have no
// plaintext column anymore (migrations/auth/000014_cryptox_contract), so
// every read/write here needs the ring to even function; email lookup
// needs the deterministic digest key too, since there is no plaintext
// column left to fall back to. The caller (cmd/auth-service/main.go) now
// builds this ring unconditionally at boot and fails the process if it
// can't — the same "money-safety, never optional" posture T3's
// LedgerIdempotency ring already established, applied here to identity
// data instead of financial data.
func NewUserRepository(db database.DatabaseSQL, ring *cryptox.Ring, lookup *cryptox.LookupKey) UserRepository {
	if ring == nil || lookup == nil {
		panic("auth: NewUserRepository requires a non-nil cryptox ring and lookup key")
	}
	return &userRepo{db: db, ring: ring, lookup: lookup}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func emailAAD(userID uuid.UUID) cryptox.AAD {
	return cryptox.AAD{Service: "auth", Table: "auth_users", Column: "email", RowID: userID.String()}
}

func fullNameAAD(userID uuid.UUID) cryptox.AAD {
	return cryptox.AAD{Service: "auth", Table: "auth_users", Column: "full_name", RowID: userID.String()}
}

func (r *userRepo) CreateUser(ctx context.Context, u model.User, passwordHash string) error {
	emailCiphertext, err := r.ring.Seal(emailAAD(u.ID), []byte(u.Email))
	if err != nil {
		return fmt.Errorf("auth: encrypt email: %w", err)
	}
	fullNameCiphertext, err := r.ring.Seal(fullNameAAD(u.ID), []byte(u.FullName))
	if err != nil {
		return fmt.Errorf("auth: encrypt full_name: %w", err)
	}
	v := r.ring.CurrentVersion()
	emailDigest := r.lookup.Digest(normalizeEmail(u.Email))

	err = r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO auth_users (id, role, status, kyc_level,
				email_ciphertext, email_key_version, email_lookup_digest,
				full_name_ciphertext, full_name_key_version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			u.ID, u.Role, u.Status, u.KYCLevel,
			emailCiphertext, v, emailDigest,
			fullNameCiphertext, v); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO auth_credentials (user_id, password_hash)
			VALUES ($1, $2)`,
			u.ID, passwordHash); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if generalerror.IsDuplicateKey(err) {
			return ErrDuplicateEmail
		}
		return fmt.Errorf("auth: create user: %w", err)
	}
	return nil
}

const userColumns = `id, role, status, kyc_level, created_at, updated_at,
	email_ciphertext, full_name_ciphertext, kyc_verified_until, email_verified_at`

func (r *userRepo) scanUser(row *sql.Row) (model.User, error) {
	var u model.User
	var emailCiphertext, fullNameCiphertext []byte
	var kycVerifiedUntil, emailVerifiedAt sql.NullTime
	err := row.Scan(&u.ID, &u.Role, &u.Status, &u.KYCLevel, &u.CreatedAt, &u.UpdatedAt,
		&emailCiphertext, &fullNameCiphertext, &kycVerifiedUntil, &emailVerifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	if err != nil {
		return model.User{}, fmt.Errorf("auth: scan user: %w", err)
	}
	if kycVerifiedUntil.Valid {
		value := kycVerifiedUntil.Time
		u.KYCVerifiedUntil = &value
	}
	if emailVerifiedAt.Valid {
		value := emailVerifiedAt.Time
		u.EmailVerifiedAt = &value
	}
	plainEmail, err := r.ring.Open(emailAAD(u.ID), emailCiphertext)
	if err != nil {
		return model.User{}, fmt.Errorf("auth: decrypt email: %w", err)
	}
	u.Email = string(plainEmail)
	plainFullName, err := r.ring.Open(fullNameAAD(u.ID), fullNameCiphertext)
	if err != nil {
		return model.User{}, fmt.Errorf("auth: decrypt full_name: %w", err)
	}
	u.FullName = string(plainFullName)
	return u, nil
}

func (r *userRepo) GetUserByEmail(ctx context.Context, email string) (model.User, error) {
	digest := r.lookup.Digest(normalizeEmail(email))
	return r.scanUser(r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM auth_users WHERE email_lookup_digest = $1`, digest))
}

func (r *userRepo) GetUserByID(ctx context.Context, id uuid.UUID) (model.User, error) {
	return r.scanUser(r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM auth_users WHERE id = $1`, id))
}

func (r *userRepo) GetPasswordHash(ctx context.Context, userID uuid.UUID) (string, error) {
	var hash string
	err := r.db.QueryRowContext(ctx,
		`SELECT password_hash FROM auth_credentials WHERE user_id = $1`, userID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("auth: get password hash: %w", err)
	}
	return hash, nil
}

func (r *userRepo) UpdateFullName(ctx context.Context, userID uuid.UUID, fullName string) error {
	fullNameCiphertext, err := r.ring.Seal(fullNameAAD(userID), []byte(fullName))
	if err != nil {
		return fmt.Errorf("auth: encrypt full_name: %w", err)
	}
	v := r.ring.CurrentVersion()
	res, err := r.db.ExecContext(ctx,
		`UPDATE auth_users SET full_name_ciphertext = $1, full_name_key_version = $2, updated_at = now() WHERE id = $3`,
		fullNameCiphertext, v, userID)
	if err != nil {
		return fmt.Errorf("auth: update full name: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("auth: update full name: rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

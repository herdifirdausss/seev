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
	// GetUserByEmail looks up by lower(email). ErrNotFound when absent.
	GetUserByEmail(ctx context.Context, email string) (model.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (model.User, error)
	// GetPasswordHash returns the bcrypt hash for the user — the ONLY read
	// path for auth_credentials, used solely inside Module.Login.
	GetPasswordHash(ctx context.Context, userID uuid.UUID) (string, error)
	UpdateFullName(ctx context.Context, userID uuid.UUID, fullName string) error

	// BackfillOnce is docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.5's bounded backfill:
	// one batch of pre-expand-phase rows (email_ciphertext IS NULL) gets
	// encrypted and written in place. Returns the number of rows processed
	// — the caller loops until it returns 0, at which point the WHERE
	// clause's own emptiness IS the completion proof (no separate cursor
	// to lose track of, so a crash mid-loop just repeats the same,
	// still-correct query on restart). FOR UPDATE SKIP LOCKED makes
	// concurrent backfill runs (or an accidental double-invocation) safe —
	// no row is ever claimed twice.
	BackfillOnce(ctx context.Context, batchSize int) (int, error)
}

type userRepo struct {
	db     database.DatabaseSQL
	ring   *cryptox.Ring
	lookup *cryptox.LookupKey
}

// NewUserRepository's ring/lookup are docs/roadmap/active/51-a8-data-lifecycle-privacy.md T2.3's
// K2/K3 expand-phase encryption: both nil (Module never configured
// cryptox) means every read/write here behaves exactly as before this
// task — plaintext columns only. A non-nil ring makes CreateUser/
// UpdateFullName dual-write ciphertext alongside plaintext (K3 step 2);
// GetUserByEmail/scanUser dual-read (ciphertext when present, plaintext
// fallback for any row not yet backfilled — docs/roadmap/active/51 T2.5's job, not this
// one). lookup may be nil even when ring isn't — deterministic email
// lookup needs its own separate key (K2), so a ring without a configured
// lookup key still encrypts writes but GetUserByEmail falls back to the
// plaintext-only query path.
func NewUserRepository(db database.DatabaseSQL, ring *cryptox.Ring, lookup *cryptox.LookupKey) UserRepository {
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
	var emailCiphertext, fullNameCiphertext, emailDigest []byte
	var emailKeyVersion, fullNameKeyVersion *int
	if r.ring != nil {
		var err error
		if emailCiphertext, err = r.ring.Seal(emailAAD(u.ID), []byte(u.Email)); err != nil {
			return fmt.Errorf("auth: encrypt email: %w", err)
		}
		if fullNameCiphertext, err = r.ring.Seal(fullNameAAD(u.ID), []byte(u.FullName)); err != nil {
			return fmt.Errorf("auth: encrypt full_name: %w", err)
		}
		v := r.ring.CurrentVersion()
		emailKeyVersion, fullNameKeyVersion = &v, &v
		if r.lookup != nil {
			emailDigest = r.lookup.Digest(normalizeEmail(u.Email))
		}
	}

	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO auth_users (id, email, full_name, role, status, kyc_level,
				email_ciphertext, email_key_version, email_lookup_digest,
				full_name_ciphertext, full_name_key_version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			u.ID, u.Email, u.FullName, u.Role, u.Status, u.KYCLevel,
			emailCiphertext, emailKeyVersion, emailDigest,
			fullNameCiphertext, fullNameKeyVersion); err != nil {
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

const userColumns = `id, email, full_name, role, status, kyc_level, created_at, updated_at,
	email_ciphertext, email_key_version, full_name_ciphertext, full_name_key_version`

func (r *userRepo) scanUser(row *sql.Row) (model.User, error) {
	var u model.User
	var emailCiphertext, fullNameCiphertext []byte
	var emailKeyVersion, fullNameKeyVersion *int
	err := row.Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.Status, &u.KYCLevel, &u.CreatedAt, &u.UpdatedAt,
		&emailCiphertext, &emailKeyVersion, &fullNameCiphertext, &fullNameKeyVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	if err != nil {
		return model.User{}, fmt.Errorf("auth: scan user: %w", err)
	}
	// Dual-read (K3): a row already backfilled has ciphertext and it wins;
	// a row not yet backfilled has none and the plaintext column already
	// scanned above stands as-is. ring == nil (cryptox unconfigured) never
	// attempts to decrypt, even if a ciphertext column happens to be
	// populated from a differently-configured process.
	if r.ring != nil && emailCiphertext != nil {
		plain, err := r.ring.Open(emailAAD(u.ID), emailCiphertext)
		if err != nil {
			return model.User{}, fmt.Errorf("auth: decrypt email: %w", err)
		}
		u.Email = string(plain)
	}
	if r.ring != nil && fullNameCiphertext != nil {
		plain, err := r.ring.Open(fullNameAAD(u.ID), fullNameCiphertext)
		if err != nil {
			return model.User{}, fmt.Errorf("auth: decrypt full_name: %w", err)
		}
		u.FullName = string(plain)
	}
	return u, nil
}

func (r *userRepo) GetUserByEmail(ctx context.Context, email string) (model.User, error) {
	if r.ring != nil && r.lookup != nil {
		digest := r.lookup.Digest(normalizeEmail(email))
		u, err := r.scanUser(r.db.QueryRowContext(ctx,
			`SELECT `+userColumns+` FROM auth_users WHERE email_lookup_digest = $1`, digest))
		if err == nil {
			return u, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return model.User{}, err
		}
		// Not found by digest — either genuinely absent, or a row that
		// predates the lookup-digest backfill (K3's dual-read window).
		// Fall through to the plaintext path below rather than returning
		// ErrNotFound prematurely.
	}
	return r.scanUser(r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM auth_users WHERE lower(email) = lower($1)`, email))
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
	var fullNameCiphertext []byte
	var fullNameKeyVersion *int
	if r.ring != nil {
		var err error
		if fullNameCiphertext, err = r.ring.Seal(fullNameAAD(userID), []byte(fullName)); err != nil {
			return fmt.Errorf("auth: encrypt full_name: %w", err)
		}
		v := r.ring.CurrentVersion()
		fullNameKeyVersion = &v
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE auth_users SET full_name = $1, full_name_ciphertext = $2, full_name_key_version = $3, updated_at = now() WHERE id = $4`,
		fullName, fullNameCiphertext, fullNameKeyVersion, userID)
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

func (r *userRepo) BackfillOnce(ctx context.Context, batchSize int) (int, error) {
	if r.ring == nil {
		return 0, fmt.Errorf("auth: cryptox ring not configured, cannot backfill")
	}
	type pendingRow struct {
		id       uuid.UUID
		email    string
		fullName string
	}
	var rows []pendingRow
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		result, queryErr := tx.QueryContext(ctx, `
			SELECT id, email, full_name FROM auth_users
			WHERE email_ciphertext IS NULL
			ORDER BY created_at, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED`, batchSize)
		if queryErr != nil {
			return fmt.Errorf("select backfill batch: %w", queryErr)
		}
		for result.Next() {
			var pr pendingRow
			if scanErr := result.Scan(&pr.id, &pr.email, &pr.fullName); scanErr != nil {
				result.Close()
				return fmt.Errorf("scan backfill row: %w", scanErr)
			}
			rows = append(rows, pr)
		}
		if rowsErr := result.Err(); rowsErr != nil {
			result.Close()
			return fmt.Errorf("iterate backfill batch: %w", rowsErr)
		}
		result.Close()

		v := r.ring.CurrentVersion()
		for _, pr := range rows {
			emailCiphertext, sealErr := r.ring.Seal(emailAAD(pr.id), []byte(pr.email))
			if sealErr != nil {
				return fmt.Errorf("encrypt email for backfill %s: %w", pr.id, sealErr)
			}
			fullNameCiphertext, sealErr := r.ring.Seal(fullNameAAD(pr.id), []byte(pr.fullName))
			if sealErr != nil {
				return fmt.Errorf("encrypt full_name for backfill %s: %w", pr.id, sealErr)
			}
			var digest []byte
			if r.lookup != nil {
				digest = r.lookup.Digest(normalizeEmail(pr.email))
			}
			if _, execErr := tx.ExecContext(ctx, `
				UPDATE auth_users
				SET email_ciphertext = $1, email_key_version = $2, email_lookup_digest = COALESCE($3, email_lookup_digest),
				    full_name_ciphertext = $4, full_name_key_version = $5
				WHERE id = $6`,
				emailCiphertext, v, digest, fullNameCiphertext, v, pr.id); execErr != nil {
				return fmt.Errorf("update backfilled user %s: %w", pr.id, execErr)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

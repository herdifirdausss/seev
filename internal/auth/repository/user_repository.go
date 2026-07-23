package repository

//go:generate mockgen -source=user_repository.go -destination=user_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/auth/model"
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
}

type userRepo struct {
	db database.DatabaseSQL
}

func NewUserRepository(db database.DatabaseSQL) UserRepository {
	return &userRepo{db: db}
}

func (r *userRepo) CreateUser(ctx context.Context, u model.User, passwordHash string) error {
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO auth_users (id, email, full_name, role, status, kyc_level)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			u.ID, u.Email, u.FullName, u.Role, u.Status, u.KYCLevel); err != nil {
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

const userColumns = `id, email, full_name, role, status, kyc_level, created_at, updated_at`

func scanUser(row *sql.Row) (model.User, error) {
	var u model.User
	err := row.Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.Status, &u.KYCLevel, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	if err != nil {
		return model.User{}, fmt.Errorf("auth: scan user: %w", err)
	}
	return u, nil
}

func (r *userRepo) GetUserByEmail(ctx context.Context, email string) (model.User, error) {
	return scanUser(r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM auth_users WHERE lower(email) = lower($1)`, email))
}

func (r *userRepo) GetUserByID(ctx context.Context, id uuid.UUID) (model.User, error) {
	return scanUser(r.db.QueryRowContext(ctx,
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
	res, err := r.db.ExecContext(ctx,
		`UPDATE auth_users SET full_name = $1, updated_at = now() WHERE id = $2`, fullName, userID)
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

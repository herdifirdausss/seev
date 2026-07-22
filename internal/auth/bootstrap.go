package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/internal/auth/repository"
)

// EnsureBootstrapAdmin idempotently creates the first admin account from
// env config (docs/plan/25 T1 step 6) — called once at startup by the
// composition root. Chosen over a seed migration so no password hash is
// ever committed to VCS. No-op when the email already exists.
func (m *Module) EnsureBootstrapAdmin(ctx context.Context, email, password string) error {
	return m.ensureBootstrapOperator(ctx, email, password, model.RoleAdmin, "Bootstrap Admin")
}

// EnsureBootstrapOperator creates an optional maker/checker bootstrap account
// without ever storing credentials in source or migrations.
func (m *Module) EnsureBootstrapOperator(ctx context.Context, email, password, role string) error {
	fullName := "Bootstrap " + role
	return m.ensureBootstrapOperator(ctx, email, password, role, fullName)
}

func (m *Module) ensureBootstrapOperator(ctx context.Context, email, password, role, fullName string) error {
	if email == "" || password == "" {
		return nil // bootstrap admin not configured — fine
	}
	if _, err := m.repo.GetUserByEmail(ctx, email); err == nil {
		return nil // already exists
	} else if !errors.Is(err, repository.ErrNotFound) {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return fmt.Errorf("auth: hash bootstrap admin password: %w", err)
	}
	u := model.User{
		ID: uuid.New(), Email: email, FullName: fullName,
		Role: role, Status: model.StatusActive, KYCLevel: 2,
	}
	if err := m.repo.CreateUser(ctx, u, string(hash)); err != nil {
		if errors.Is(err, repository.ErrDuplicateEmail) {
			return nil // raced another replica — fine, it exists
		}
		return err
	}
	m.logger.Info("auth: bootstrap operator created", slog.String("email", email), slog.String("role", role))
	return nil
}

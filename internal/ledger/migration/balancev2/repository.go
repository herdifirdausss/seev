package balancev2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/pkg/database"
)

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readSource(ctx context.Context, q queryer, accountID uuid.UUID) (SourceRow, error) {
	var row SourceRow
	err := q.QueryRowContext(ctx, `
		SELECT ab.account_id, a.currency, a.type, ab.balance,
		       ab.allow_negative, ab.version, ab.updated_at
		FROM account_balances ab
		JOIN accounts a ON a.id = ab.account_id
		WHERE ab.account_id = $1`, accountID).Scan(
		&row.AccountID, &row.Currency, &row.AccountType, &row.Balance,
		&row.AllowNegative, &row.SourceVersion, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceRow{}, ErrMigrationNotFound
	}
	if err != nil {
		return SourceRow{}, fmt.Errorf("balancev2: read source %s: %w", accountID, err)
	}
	return row, nil
}

func readSourceForUpdate(ctx context.Context, q queryer, accountID uuid.UUID) (SourceRow, error) {
	var row SourceRow
	err := q.QueryRowContext(ctx, `
		SELECT ab.account_id, a.currency, a.type, ab.balance,
		       ab.allow_negative, ab.version, ab.updated_at
		FROM account_balances ab
		JOIN accounts a ON a.id = ab.account_id
		WHERE ab.account_id = $1
		FOR UPDATE OF ab`, accountID).Scan(
		&row.AccountID, &row.Currency, &row.AccountType, &row.Balance,
		&row.AllowNegative, &row.SourceVersion, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceRow{}, ErrMigrationNotFound
	}
	if err != nil {
		return SourceRow{}, fmt.Errorf("balancev2: lock source %s: %w", accountID, err)
	}
	return row, nil
}

func readSourceBatch(ctx context.Context, q queryer, lastKey *uuid.UUID, limit int) ([]SourceRow, error) {
	if limit <= 0 {
		limit = 100
	}
	var lastArg any
	if lastKey != nil {
		lastArg = *lastKey
	}
	rows, err := q.QueryContext(ctx, `
		SELECT ab.account_id, a.currency, a.type, ab.balance,
		       ab.allow_negative, ab.version, ab.updated_at
		FROM account_balances ab
		JOIN accounts a ON a.id = ab.account_id
		WHERE ($1::uuid IS NULL OR ab.account_id > $1)
		ORDER BY ab.account_id
		LIMIT $2`, lastArg, limit)
	if err != nil {
		return nil, fmt.Errorf("balancev2: read source batch: %w", err)
	}
	defer rows.Close()
	result := make([]SourceRow, 0, limit)
	for rows.Next() {
		var row SourceRow
		if err := rows.Scan(&row.AccountID, &row.Currency, &row.AccountType, &row.Balance, &row.AllowNegative, &row.SourceVersion, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("balancev2: scan source batch: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("balancev2: iterate source batch: %w", err)
	}
	return result, nil
}

func targetForAccount(ctx context.Context, q queryer, accountID uuid.UUID) (*TargetRow, error) {
	var row TargetRow
	var lastID sql.NullString
	var sourceCurrency, sourceType string
	var sourceAllowNegative bool
	err := q.QueryRowContext(ctx, `
		SELECT v.account_id, v.account_type, v.currency, a.status,
		       a.currency, a.type,
		       v.allow_negative, v.available_amount, v.reserved_amount,
		       v.pending_amount, v.restricted_amount, v.source_version,
		       v.last_transaction_id::text, v.projection_checksum,
		       v.created_at, v.updated_at
		FROM account_balances_v2 v
		JOIN accounts a ON a.id = v.account_id
		JOIN account_balances ab ON ab.account_id = v.account_id
		WHERE v.account_id = $1`, accountID).Scan(
		&row.AccountID, &row.AccountType, &row.Currency, &row.Status,
		&sourceCurrency, &sourceType, &sourceAllowNegative,
		&row.AllowNegative, &row.AvailableAmount, &row.ReservedAmount,
		&row.PendingAmount, &row.RestrictedAmount, &row.SourceVersion,
		&lastID, &row.ProjectionChecksum, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("balancev2: read target %s: %w", accountID, err)
	}
	if strings.ToUpper(strings.TrimSpace(row.Currency)) != strings.ToUpper(strings.TrimSpace(sourceCurrency)) ||
		strings.ToLower(strings.TrimSpace(row.AccountType)) != strings.ToLower(strings.TrimSpace(sourceType)) ||
		row.AllowNegative != sourceAllowNegative {
		return nil, fmt.Errorf("balancev2: target semantic identity mismatch for %s", accountID)
	}
	if lastID.Valid && strings.TrimSpace(lastID.String) != "" {
		id, parseErr := uuid.Parse(lastID.String)
		if parseErr != nil {
			return nil, fmt.Errorf("balancev2: parse target transaction id: %w", parseErr)
		}
		row.LastTransactionID = &id
	}
	return &row, nil
}

func upsertTarget(ctx context.Context, tx *sql.Tx, target TargetRow) (bool, error) {
	var lastTransaction any
	if target.LastTransactionID != nil && *target.LastTransactionID != uuid.Nil {
		lastTransaction = *target.LastTransactionID
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO account_balances_v2 (
			account_id, account_type, currency, allow_negative,
			available_amount, reserved_amount, pending_amount, restricted_amount,
			source_version, last_transaction_id, projection_checksum, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now(), now())
		ON CONFLICT (account_id) DO UPDATE SET
			account_type = EXCLUDED.account_type,
			currency = EXCLUDED.currency,
			allow_negative = EXCLUDED.allow_negative,
			available_amount = EXCLUDED.available_amount,
			reserved_amount = EXCLUDED.reserved_amount,
			pending_amount = EXCLUDED.pending_amount,
			restricted_amount = EXCLUDED.restricted_amount,
			source_version = EXCLUDED.source_version,
			last_transaction_id = EXCLUDED.last_transaction_id,
			projection_checksum = EXCLUDED.projection_checksum,
			updated_at = now()
		WHERE account_balances_v2.source_version < EXCLUDED.source_version
		   OR (account_balances_v2.source_version = EXCLUDED.source_version
		       AND account_balances_v2.last_transaction_id IS NULL)`,
		target.AccountID, target.AccountType, target.Currency, target.AllowNegative,
		target.AvailableAmount, target.ReservedAmount, target.PendingAmount,
		target.RestrictedAmount, target.SourceVersion, lastTransaction,
		target.ProjectionChecksum)
	if err != nil {
		return false, fmt.Errorf("balancev2: upsert target %s: %w", target.AccountID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("balancev2: target rows affected: %w", err)
	}
	return affected > 0, nil
}

func replaceTarget(ctx context.Context, tx *sql.Tx, target TargetRow) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO account_balances_v2 (
			account_id, account_type, currency, allow_negative,
			available_amount, reserved_amount, pending_amount, restricted_amount,
			source_version, last_transaction_id, projection_checksum, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULL, $10, now(), now())
		ON CONFLICT (account_id) DO UPDATE SET
			account_type = EXCLUDED.account_type,
			currency = EXCLUDED.currency,
			allow_negative = EXCLUDED.allow_negative,
			available_amount = EXCLUDED.available_amount,
			reserved_amount = EXCLUDED.reserved_amount,
			pending_amount = EXCLUDED.pending_amount,
			restricted_amount = EXCLUDED.restricted_amount,
			source_version = EXCLUDED.source_version,
			last_transaction_id = NULL,
			projection_checksum = EXCLUDED.projection_checksum,
			updated_at = now()
		WHERE account_balances_v2.source_version <= EXCLUDED.source_version`,
		target.AccountID, target.AccountType, target.Currency, target.AllowNegative,
		target.AvailableAmount, target.ReservedAmount, target.PendingAmount,
		target.RestrictedAmount, target.SourceVersion, target.ProjectionChecksum)
	if err != nil {
		return fmt.Errorf("balancev2: replace target %s: %w", target.AccountID, err)
	}
	return nil
}

func countSource(ctx context.Context, db database.DatabaseSQL) (int64, error) {
	var count int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM account_balances`).Scan(&count); err != nil {
		return 0, fmt.Errorf("balancev2: count source rows: %w", err)
	}
	return count, nil
}

func countTarget(ctx context.Context, db database.DatabaseSQL) (int64, error) {
	var count int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM account_balances_v2`).Scan(&count); err != nil {
		return 0, fmt.Errorf("balancev2: count target rows: %w", err)
	}
	return count, nil
}

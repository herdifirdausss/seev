package fx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/currency"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// ListPositions returns the synthetic FX position account state grouped by
// pair and currency. It never produces a cross-currency total.
func (s *Service) ListPositions(ctx context.Context) ([]model.FXPosition, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.pair_id, p.pair_code, l.currency, a.id, c.minor_unit,
		       COALESCE(ab.balance, 0), l.minimum_balance, l.maximum_balance,
		       COALESCE(l.warning_minimum_balance, l.minimum_balance),
		       COALESCE(l.warning_maximum_balance, l.maximum_balance),
		       COALESCE(l.critical_minimum_balance, l.minimum_balance),
		       COALESCE(l.critical_maximum_balance, l.maximum_balance),
		       (
		           SELECT MAX(COALESCE(fc.posted_at, fc.created_at))
		           FROM fx_conversions fc
		           JOIN fx_quotes fq ON fq.id = fc.quote_id
		           WHERE fq.pair_id = l.pair_id
		             AND fc.status = 'posted'
		             AND (fc.source_currency = l.currency OR fc.target_currency = l.currency)
		       )
		FROM fx_position_limits l
		JOIN fx_pairs p ON p.id = l.pair_id
		JOIN currencies c ON c.code = l.currency
		LEFT JOIN accounts a
		  ON a.owner_type = 'system' AND a.type = 'fx_conversion'
		 AND a.currency = l.currency AND a.system_qualifier = p.position_qualifier
		 AND a.status = 'active'
		LEFT JOIN account_balances ab ON ab.account_id = a.id
		ORDER BY p.pair_code, l.currency`)
	if err != nil {
		return nil, fmt.Errorf("list FX positions: %w", err)
	}
	defer rows.Close()

	positions := make([]model.FXPosition, 0)
	for rows.Next() {
		var position model.FXPosition
		var pairCode, code string
		var accountID uuid.NullUUID
		var lastConversionAt sql.NullTime
		if err := rows.Scan(
			&position.PairID, &pairCode, &code, &accountID, &position.MinorUnit,
			&position.Balance, &position.MinimumBalance, &position.MaximumBalance,
			&position.WarningMinimumBalance, &position.WarningMaximumBalance,
			&position.CriticalMinimumBalance, &position.CriticalMaximumBalance,
			&lastConversionAt,
		); err != nil {
			return nil, fmt.Errorf("scan FX position: %w", err)
		}
		position.PairCode = strings.TrimSpace(pairCode)
		position.Currency = strings.TrimSpace(code)
		if accountID.Valid {
			position.AccountID = accountID.UUID
		} else {
			position.State = "critical"
		}
		if lastConversionAt.Valid {
			position.LastConversionAt = &lastConversionAt.Time
		}
		if position.State == "" {
			position.State = positionState(position.Balance, position.MinimumBalance, position.MaximumBalance,
				position.WarningMinimumBalance, position.WarningMaximumBalance,
				position.CriticalMinimumBalance, position.CriticalMaximumBalance)
		}
		observePositionMetrics(position.PairCode, position.Currency, position.Balance,
			position.MinimumBalance, position.MaximumBalance,
			position.WarningMinimumBalance, position.WarningMaximumBalance,
			position.CriticalMinimumBalance, position.CriticalMaximumBalance)
		positions = append(positions, position)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate FX positions: %w", err)
	}
	return positions, nil
}

// UpdateCurrencyPolicy changes the database-owned lifecycle and operation
// capabilities for one currency. The runtime registry is refreshed after the
// durable update so amount metadata and in-flight work observe the same
// currency catalogue without requiring a process restart.
func (s *Service) UpdateCurrencyPolicy(ctx context.Context, code, status string, requested map[string]bool, actor string) error {
	if err := requireAdminActor(actor); err != nil {
		return err
	}
	code, err := canonicalCode(code)
	if err != nil {
		return err
	}
	policy, err := loadCurrencyPolicy(ctx, s.db, code)
	if err != nil {
		return err
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = policy.Status
	}
	switch status {
	case "draft", "active", "intake_paused", "disabled":
	default:
		return fmt.Errorf("%w: unsupported currency status %q", apperror.ErrValidation, status)
	}
	if status == "draft" && policy.Status != "draft" {
		return fmt.Errorf("%w: an onboarded currency cannot return to draft", apperror.ErrValidation)
	}

	operations := cloneOperations(policy.Operations)
	if operations == nil {
		operations = cloneOperations(defaultOperations)
	}
	for operation, enabled := range requested {
		if _, ok := defaultOperations[operation]; !ok {
			return fmt.Errorf("%w: unsupported currency operation %q", apperror.ErrValidation, operation)
		}
		operations[operation] = enabled
	}
	rawOperations, err := json.Marshal(operations)
	if err != nil {
		return fmt.Errorf("encode currency operations: %w", err)
	}
	enabled := status == "active" || status == "intake_paused"
	result, err := s.db.ExecContext(ctx, `
		UPDATE currencies
		SET status = $2, enabled = $3, operations = $4::jsonb
		WHERE code = $1`, code, status, enabled, string(rawOperations))
	if err != nil {
		return fmt.Errorf("update currency policy: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read currency policy result: %w", err)
	}
	if changed == 0 {
		return fmt.Errorf("%w: currency %s", apperror.ErrValidation, code)
	}
	if err := s.refreshCurrencyRegistry(ctx); err != nil {
		return fmt.Errorf("refresh currency registry: %w", err)
	}
	return nil
}

func (s *Service) UpdatePairStatus(ctx context.Context, pairID uuid.UUID, status, actor string) error {
	if pairID == uuid.Nil {
		return fmt.Errorf("%w: pair id is required", apperror.ErrValidation)
	}
	if err := requireAdminActor(actor); err != nil {
		return err
	}
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "draft", "active", "paused", "disabled":
	default:
		return fmt.Errorf("%w: unsupported FX pair status %q", apperror.ErrValidation, status)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE fx_pairs SET status = $2 WHERE id = $1`, pairID, status)
	if err != nil {
		return fmt.Errorf("update FX pair status: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read FX pair status result: %w", err)
	}
	if changed == 0 {
		return apperror.ErrFXPairUnavailable
	}
	return nil
}

func (s *Service) UpdateDirectionControls(ctx context.Context, directionID uuid.UUID, enabled, newQuotesPaused, conversionsPaused bool, minimum, maximum, spreadBasisPoints int64, actor string) error {
	if directionID == uuid.Nil {
		return fmt.Errorf("%w: direction id is required", apperror.ErrValidation)
	}
	if err := requireAdminActor(actor); err != nil {
		return err
	}
	if minimum <= 0 || maximum <= minimum {
		return fmt.Errorf("%w: direction amount bounds are invalid", apperror.ErrCurrencyLimitExceeded)
	}
	if spreadBasisPoints < 0 || spreadBasisPoints > 9999 {
		return fmt.Errorf("%w: spread basis points must be between 0 and 9999", apperror.ErrValidation)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE fx_pair_directions
		SET enabled = $2,
		    new_quotes_paused = $3,
		    conversions_paused = $4,
		    min_source_amount = $5,
		    max_source_amount = $6,
		    spread_basis_points = $7
		WHERE id = $1`, directionID, enabled, newQuotesPaused, conversionsPaused, minimum, maximum, spreadBasisPoints)
	if err != nil {
		return fmt.Errorf("update FX direction controls: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read FX direction control result: %w", err)
	}
	if changed == 0 {
		return apperror.ErrFXPairUnavailable
	}
	return nil
}

func (s *Service) ListRateVersions(ctx context.Context, pairID, directionID uuid.UUID) ([]model.FXRateVersion, error) {
	if pairID == uuid.Nil {
		return nil, fmt.Errorf("%w: pair id is required", apperror.ErrValidation)
	}
	query := `
		SELECT r.id, r.pair_id, r.direction_id, r.version, r.reference_rate::text,
		       r.rate_source,
		       CASE WHEN r.status = 'active' AND r.effective_to IS NOT NULL AND r.effective_to <= now()
		            THEN 'expired' ELSE r.status END,
		       r.effective_from, r.effective_to,
		       r.created_by, r.submitted_by, r.approved_by, r.created_at,
		       r.submitted_at, r.approved_at, r.retired_at
		FROM fx_rate_versions r
		WHERE r.pair_id = $1`
	args := []any{pairID}
	if directionID != uuid.Nil {
		query += ` AND r.direction_id = $2`
		args = append(args, directionID)
	}
	query += ` ORDER BY r.direction_id, r.version DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list FX rate versions: %w", err)
	}
	defer rows.Close()

	result := make([]model.FXRateVersion, 0)
	for rows.Next() {
		version, err := scanFXRateVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("scan FX rate version: %w", err)
		}
		result = append(result, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate FX rate versions: %w", err)
	}
	return result, nil
}

// UpdatePairControls changes the pair-wide intake gates for every configured
// direction. The operation is intentionally separate from rate publication so
// operators can stop new quotes without invalidating already issued quotes.
func (s *Service) UpdatePairControls(ctx context.Context, pairID uuid.UUID, newQuotesPaused, conversionsPaused bool, actor string) error {
	if pairID == uuid.Nil {
		return fmt.Errorf("%w: pair id is required", apperror.ErrValidation)
	}
	if err := requireAdminActor(actor); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE fx_pair_directions
		SET new_quotes_paused = $2, conversions_paused = $3
		WHERE pair_id = $1`, pairID, newQuotesPaused, conversionsPaused)
	if err != nil {
		return fmt.Errorf("update FX pair controls: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read FX pair control result: %w", err)
	}
	if changed == 0 {
		return apperror.ErrFXPairUnavailable
	}
	return nil
}

func (s *Service) UpdatePositionLimit(ctx context.Context, pairID uuid.UUID, code string, minimum, maximum int64, actor string) error {
	if pairID == uuid.Nil {
		return fmt.Errorf("%w: pair id is required", apperror.ErrValidation)
	}
	if err := requireAdminActor(actor); err != nil {
		return err
	}
	code, err := canonicalCode(code)
	if err != nil {
		return err
	}
	if minimum >= maximum {
		return fmt.Errorf("%w: minimum balance must be less than maximum balance", apperror.ErrCurrencyLimitExceeded)
	}
	var supported bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM fx_pairs
			WHERE id = $1 AND status <> 'disabled'
			  AND $2 IN (base_currency, quote_currency)
		)`, pairID, code).Scan(&supported); err != nil {
		return fmt.Errorf("validate FX position currency: %w", err)
	}
	if !supported {
		return apperror.ErrFXPairUnavailable
	}
	warningMinimum, warningMaximum, criticalMinimum, criticalMaximum := positionBands(minimum, maximum)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO fx_position_limits (
			pair_id, currency, minimum_balance, maximum_balance,
			warning_minimum_balance, warning_maximum_balance,
			critical_minimum_balance, critical_maximum_balance
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (pair_id, currency) DO UPDATE
		SET minimum_balance = EXCLUDED.minimum_balance,
		    maximum_balance = EXCLUDED.maximum_balance,
		    warning_minimum_balance = EXCLUDED.warning_minimum_balance,
		    warning_maximum_balance = EXCLUDED.warning_maximum_balance,
		    critical_minimum_balance = EXCLUDED.critical_minimum_balance,
		    critical_maximum_balance = EXCLUDED.critical_maximum_balance`,
		pairID, code, minimum, maximum, warningMinimum, warningMaximum, criticalMinimum, criticalMaximum); err != nil {
		return fmt.Errorf("update FX position limit: %w", err)
	}
	return nil
}

// ResolveRebalanceTarget validates an operator-selected pair/currency and
// returns the internal position-account qualifier. The public/admin contract
// uses pair_id; processors use the immutable qualifier so a pair code rename
// cannot redirect a pending rebalance to another account family.
func (s *Service) ResolveRebalanceTarget(ctx context.Context, pairID uuid.UUID, code string) (string, string, error) {
	if pairID == uuid.Nil {
		return "", "", fmt.Errorf("%w: pair id is required", apperror.ErrValidation)
	}
	code, err := canonicalCode(code)
	if err != nil {
		return "", "", err
	}
	var qualifier string
	err = s.db.QueryRowContext(ctx, `
		SELECT p.position_qualifier
		FROM fx_pairs p
		WHERE p.id = $1
		  AND p.status <> 'disabled'
		  AND $2 IN (p.base_currency, p.quote_currency)
		  AND EXISTS (
			  SELECT 1 FROM accounts a
			  WHERE a.owner_type = 'system' AND a.type = 'fx_conversion'
			    AND a.system_qualifier = p.position_qualifier
			    AND a.currency = $2 AND a.status = 'active'
		  )
		  AND EXISTS (
			  SELECT 1 FROM accounts a
			  WHERE a.owner_type = 'system' AND a.type = 'adjustment'
			    AND a.currency = $2 AND a.status = 'active'
		  )`, pairID, code).Scan(&qualifier)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", apperror.ErrFXPairUnavailable
	}
	if err != nil {
		return "", "", fmt.Errorf("resolve FX rebalance target: %w", err)
	}
	return strings.TrimSpace(qualifier), code, nil
}

func (s *Service) CreateRate(ctx context.Context, pairID, directionID uuid.UUID, referenceRate string, effectiveFrom time.Time, effectiveTo *time.Time, actor string) (model.FXRateVersion, error) {
	if pairID == uuid.Nil || directionID == uuid.Nil {
		return model.FXRateVersion{}, fmt.Errorf("%w: pair and direction ids are required", apperror.ErrValidation)
	}
	if err := requireAdminActor(actor); err != nil {
		return model.FXRateVersion{}, err
	}
	parsedRate, err := currency.ParseRate(referenceRate)
	if err != nil {
		return model.FXRateVersion{}, fmt.Errorf("%w: %v", apperror.ErrFXRateInvalid, err)
	}
	storedRate, err := parsedRate.DecimalString(18)
	if err != nil {
		return model.FXRateVersion{}, fmt.Errorf("%w: %v", apperror.ErrFXRateInvalid, err)
	}
	if effectiveFrom.IsZero() {
		effectiveFrom = s.now()
	}
	if effectiveTo != nil && !effectiveTo.After(effectiveFrom) {
		return model.FXRateVersion{}, fmt.Errorf("%w: effective_to must be after effective_from", apperror.ErrFXRateInvalid)
	}

	var result model.FXRateVersion
	err = s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var rateSource string
		if err := tx.QueryRowContext(ctx, `
			SELECT p.rate_source
			FROM fx_pairs p
			JOIN fx_pair_directions d ON d.pair_id = p.id
			WHERE p.id = $1 AND d.id = $2`, pairID, directionID).Scan(&rateSource); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return apperror.ErrFXPairUnavailable
			}
			return fmt.Errorf("resolve FX rate pair: %w", err)
		}
		var version int64
		if err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM fx_pair_directions
			WHERE id = $1 AND pair_id = $2
			FOR UPDATE`, directionID, pairID).Scan(new(int)); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return apperror.ErrFXPairUnavailable
			}
			return fmt.Errorf("lock FX rate direction: %w", err)
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(version), 0) + 1
			FROM fx_rate_versions
			WHERE direction_id = $1`, directionID).Scan(&version); err != nil {
			return fmt.Errorf("allocate FX rate version: %w", err)
		}
		id := generalutil.NewV7()
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO fx_rate_versions (
				id, pair_id, direction_id, version, reference_rate, rate_source,
				status, effective_from, effective_to, created_by
			) VALUES ($1, $2, $3, $4, $5, $6, 'draft', $7, $8, $9)
			RETURNING created_at`, id, pairID, directionID, version, storedRate,
				rateSource, effectiveFrom, effectiveTo, actor).Scan(&result.CreatedAt); err != nil {
			return fmt.Errorf("insert FX rate version: %w", err)
		}
		result = model.FXRateVersion{
			ID: id, PairID: pairID, DirectionID: directionID, Version: version,
			ReferenceRate: storedRate, RateSource: rateSource, Status: "draft",
			EffectiveFrom: effectiveFrom, EffectiveTo: effectiveTo, CreatedBy: actor,
			CreatedAt: result.CreatedAt,
		}
		return nil
	})
	if err != nil {
		return model.FXRateVersion{}, err
	}
	return result, nil
}

func (s *Service) SubmitRate(ctx context.Context, rateID uuid.UUID, actor string) (model.FXRateVersion, error) {
	return s.transitionRate(ctx, rateID, actor, "draft", "pending_approval", `
		SET status = 'pending_approval', submitted_by = $1, submitted_at = $2`)
}

func (s *Service) ApproveRate(ctx context.Context, rateID uuid.UUID, actor string) (model.FXRateVersion, error) {
	if err := requireAdminActor(actor); err != nil {
		return model.FXRateVersion{}, err
	}
	if rateID == uuid.Nil {
		return model.FXRateVersion{}, fmt.Errorf("%w: rate id is required", apperror.ErrValidation)
	}
	var result model.FXRateVersion
	err := s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var rate model.FXRateVersion
		if err := scanFXRateVersion(tx.QueryRowContext(ctx, `
			SELECT r.id, r.pair_id, r.direction_id, r.version, r.reference_rate::text,
			       r.rate_source, r.status, r.effective_from, r.effective_to,
			       r.created_by, r.submitted_by, r.approved_by, r.created_at,
			       r.submitted_at, r.approved_at, r.retired_at
			FROM fx_rate_versions r WHERE r.id = $1 FOR UPDATE`, rateID), &rate); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return apperror.ErrFXRateNotFound
			}
			return fmt.Errorf("lock FX rate version: %w", err)
		}
		if rate.Status != "pending_approval" {
			return fmt.Errorf("%w: rate status is %s", apperror.ErrFXRateInvalid, rate.Status)
		}
		if rate.CreatedBy == actor || rate.SubmittedBy == actor {
			return apperror.ErrFXRateApprovalConflict
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM fx_pair_directions
			WHERE id = $1 AND pair_id = $2
			FOR UPDATE`, rate.DirectionID, rate.PairID).Scan(new(int)); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return apperror.ErrFXPairUnavailable
			}
			return fmt.Errorf("lock FX rate direction: %w", err)
		}
		var overlap bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM fx_rate_versions current_rate
				WHERE current_rate.direction_id = $1
				AND current_rate.status = 'active'
				  AND current_rate.id <> $2
				  AND current_rate.effective_from < COALESCE($4, 'infinity'::timestamptz)
				  AND (current_rate.effective_to IS NULL OR current_rate.effective_to > $3)
			)`, rate.DirectionID, rate.ID, rate.EffectiveFrom, rate.EffectiveTo).Scan(&overlap); err != nil {
			return fmt.Errorf("check FX rate overlap: %w", err)
		}
		if overlap {
			return fmt.Errorf("%w: approved rate window overlaps an existing rate", apperror.ErrFXRateInvalid)
		}
		approvedAt := s.now()
		if _, err := tx.ExecContext(ctx, `
			UPDATE fx_rate_versions
			SET status = 'active', approved_by = $1, approved_at = $2
			WHERE id = $3`, actor, approvedAt, rateID); err != nil {
			return fmt.Errorf("approve FX rate version: %w", err)
		}
		rate.Status = "active"
		rate.ApprovedBy = actor
		rate.ApprovedAt = &approvedAt
		result = rate
		return nil
	})
	if err != nil {
		return model.FXRateVersion{}, err
	}
	return result, nil
}

func (s *Service) RejectRate(ctx context.Context, rateID uuid.UUID, actor, reason string) (model.FXRateVersion, error) {
	return s.transitionRateWithReason(ctx, rateID, actor, "pending_approval", "rejected", reason)
}

func (s *Service) RetireRate(ctx context.Context, rateID uuid.UUID, actor string) (model.FXRateVersion, error) {
	return s.transitionRate(ctx, rateID, actor, "active", "retired", `
		SET status = 'retired', retired_at = $1`)
}

func (s *Service) transitionRate(ctx context.Context, rateID uuid.UUID, actor, fromStatus, toStatus, setClause string) (model.FXRateVersion, error) {
	return s.transitionRateWithReason(ctx, rateID, actor, fromStatus, toStatus, "", setClause)
}

func (s *Service) transitionRateWithReason(ctx context.Context, rateID uuid.UUID, actor, fromStatus, toStatus, reason string, clauses ...string) (model.FXRateVersion, error) {
	if rateID == uuid.Nil {
		return model.FXRateVersion{}, fmt.Errorf("%w: rate id is required", apperror.ErrValidation)
	}
	if err := requireAdminActor(actor); err != nil {
		return model.FXRateVersion{}, err
	}
	setClause := "SET status = $1"
	args := []any{toStatus}
	if len(clauses) > 0 && clauses[0] != "" {
		setClause = clauses[0]
		now := s.now()
		switch toStatus {
		case "pending_approval":
			args = []any{actor, now}
		case "retired":
			args = []any{now}
		default:
			args = []any{actor, now}
		}
	}
	if toStatus == "rejected" {
		setClause = `SET status = 'rejected'`
		if reason != "" {
			setClause += `, rejection_reason = $1`
			args = []any{reason}
		} else {
			args = nil
		}
	}
	args = append(args, rateID, fromStatus)
	query := `UPDATE fx_rate_versions ` + setClause + ` WHERE id = $` + fmt.Sprint(len(args)-1) + ` AND status = $` + fmt.Sprint(len(args)) + ` RETURNING id, pair_id, direction_id, version, reference_rate::text, rate_source, status, effective_from, effective_to, created_by, submitted_by, approved_by, created_at, submitted_at, approved_at, retired_at`
	var result model.FXRateVersion
	if err := scanFXRateVersion(s.db.QueryRowContext(ctx, query, args...)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			var exists bool
			if lookupErr := s.db.QueryRowContext(ctx,
				`SELECT EXISTS (SELECT 1 FROM fx_rate_versions WHERE id = $1)`, rateID).Scan(&exists); lookupErr == nil && !exists {
				return model.FXRateVersion{}, apperror.ErrFXRateNotFound
			}
			return model.FXRateVersion{}, apperror.ErrFXRateInvalid
		}
		return model.FXRateVersion{}, fmt.Errorf("transition FX rate version: %w", err)
	}
	return result, nil
}

func scanFXRateVersion(scanner rowScanner) (model.FXRateVersion, error) {
	var result model.FXRateVersion
	var effectiveTo, submittedAt, approvedAt, retiredAt sql.NullTime
	var submittedBy, approvedBy sql.NullString
	if err := scanner.Scan(
		&result.ID, &result.PairID, &result.DirectionID, &result.Version, &result.ReferenceRate,
		&result.RateSource, &result.Status, &result.EffectiveFrom, &effectiveTo,
		&result.CreatedBy, &submittedBy, &approvedBy, &result.CreatedAt,
		&submittedAt, &approvedAt, &retiredAt,
	); err != nil {
		return model.FXRateVersion{}, err
	}
	if effectiveTo.Valid {
		result.EffectiveTo = &effectiveTo.Time
	}
	if submittedBy.Valid {
		result.SubmittedBy = submittedBy.String
	}
	if approvedBy.Valid {
		result.ApprovedBy = approvedBy.String
	}
	if submittedAt.Valid {
		result.SubmittedAt = &submittedAt.Time
	}
	if approvedAt.Valid {
		result.ApprovedAt = &approvedAt.Time
	}
	if retiredAt.Valid {
		result.RetiredAt = &retiredAt.Time
	}
	return result, nil
}

func requireAdminActor(actor string) error {
	if strings.TrimSpace(actor) == "" {
		return fmt.Errorf("%w: operator identity is required", apperror.ErrValidation)
	}
	return nil
}

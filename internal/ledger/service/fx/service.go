// Package fx owns the currency registry projection, user currency balances,
// quote lifecycle, and atomic two-leg FX conversion orchestration for Ledger.
// It deliberately does not expose a third "combined" balance: every money
// movement remains a single-currency ledger transaction.
package fx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/currency"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/shopspring/decimal"
)

type Service struct {
	db          database.DatabaseSQL
	now         func() time.Time
	txRepo      repository.TransactionRepository
	balanceRepo repository.BalanceRepository
	entryRepo   repository.EntryRepository
	outboxRepo  repository.OutboxRepository
}

func New(
	db database.DatabaseSQL,
	txRepo repository.TransactionRepository,
	balanceRepo repository.BalanceRepository,
	entryRepo repository.EntryRepository,
	outboxRepo repository.OutboxRepository,
) *Service {
	return &Service{
		db: db, now: time.Now,
		txRepo: txRepo, balanceRepo: balanceRepo, entryRepo: entryRepo, outboxRepo: outboxRepo,
	}
}

func (s *Service) ValidateCurrency(ctx context.Context, code, operation string) error {
	err := validateCurrencyPolicy(ctx, s.db, code, operation)
	observeCurrencyOperation(operation, code, err)
	return err
}

func (s *Service) ValidateCurrencyTx(ctx context.Context, tx *sql.Tx, code, operation string) error {
	err := validateCurrencyPolicy(ctx, tx, code, operation)
	observeCurrencyOperation(operation, code, err)
	return err
}

// UserCurrencyEnabled reports whether the user owns the active cash account
// for code. It is a read-only boundary helper for journey services; account
// provisioning and all balance mutations remain owned by Ledger.
func (s *Service) UserCurrencyEnabled(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	code, err := canonicalCode(code)
	if err != nil {
		return false, err
	}
	var enabled bool
	err = s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM accounts
			WHERE owner_type = 'user' AND owner_id = $1 AND type = 'cash'
			  AND currency = $2 AND pocket_code IS NULL AND status = 'active'
		)`, userID, code).Scan(&enabled)
	if err != nil {
		return false, fmt.Errorf("check user currency account: %w", err)
	}
	return enabled, nil
}

func validateCurrencyPolicy(ctx context.Context, q queryer, code, operation string) error {
	code, err := canonicalCode(code)
	if err != nil {
		return err
	}
	policy, err := loadCurrencyPolicy(ctx, q, code)
	if err != nil {
		return err
	}
	inFlight := strings.HasSuffix(operation, "_inflight")
	policyOperation := strings.TrimSuffix(operation, "_inflight")
	if policyOperation == "statement_read" {
		if policy.Status == "draft" {
			return fmt.Errorf("%w: %s status=%s", apperror.ErrCurrencyDisabled, code, policy.Status)
		}
		if !policy.Operations["statement"] {
			return fmt.Errorf("%w: %s operation=statement", apperror.ErrCurrencyOperationDisabled, code)
		}
		return nil
	}
	if inFlight {
		// An intent/hold that was accepted while a currency was active must be
		// finishable after an intake pause or disable. Draft is intentionally
		// excluded: it was never a user-visible active currency.
		switch policy.Status {
		case "active", "intake_paused", "disabled":
			return nil
		default:
			return fmt.Errorf("%w: %s status=%s", apperror.ErrCurrencyDisabled, code, policy.Status)
		}
	}
	if !policy.Enabled || (policy.Status != "active" && policy.Status != "intake_paused") {
		return fmt.Errorf("%w: %s status=%s", apperror.ErrCurrencyDisabled, code, policy.Status)
	}
	if policy.Status == "intake_paused" && (policyOperation == "account_enable" || policyOperation == "topup") {
		return fmt.Errorf("%w: %s status=%s operation=%s", apperror.ErrCurrencyDisabled, code, policy.Status, operation)
	}
	if policyOperation != "" && !policy.Operations[policyOperation] {
		return fmt.Errorf("%w: %s operation=%s", apperror.ErrCurrencyOperationDisabled, code, policyOperation)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type currencyPolicy struct {
	Code       string
	MinorUnit  int16
	Status     string
	Enabled    bool
	Operations map[string]bool
}

var defaultOperations = map[string]bool{
	"account_enable":       true,
	"topup":                true,
	"transfer":             true,
	"payout":               true,
	"fx_source":            true,
	"fx_target":            true,
	"statement":            true,
	"notification_display": true,
}

func (s *Service) ListCurrencies(ctx context.Context, userID uuid.UUID) ([]model.CurrencyInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.code, c.minor_unit, c.status, c.operations,
		       EXISTS (
				   SELECT 1 FROM accounts a
				   WHERE a.owner_type = 'user' AND a.owner_id = $1
				     AND a.type = 'cash' AND a.currency = c.code
				     AND a.pocket_code IS NULL AND a.status = 'active'
				   ) AS user_enabled
		FROM currencies c
		ORDER BY c.code`, userID)
	if err != nil {
		return nil, fmt.Errorf("list currencies: %w", err)
	}
	defer rows.Close()

	result := make([]model.CurrencyInfo, 0)
	for rows.Next() {
		var code, status string
		var minorUnit int16
		var rawOperations []byte
		var userEnabled bool
		if err := rows.Scan(&code, &minorUnit, &status, &rawOperations, &userEnabled); err != nil {
			return nil, fmt.Errorf("scan currency: %w", err)
		}
		operations, err := decodeOperations(rawOperations)
		if err != nil {
			return nil, fmt.Errorf("decode currency %s operations: %w", code, err)
		}
		result = append(result, model.CurrencyInfo{
			Code: strings.TrimSpace(code), MinorUnit: minorUnit, Status: status,
			Operations: operations, UserEnabled: userEnabled,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate currencies: %w", err)
	}
	return result, nil
}

func (s *Service) ListBalances(ctx context.Context, userID uuid.UUID) ([]model.CurrencyBalance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.code, c.minor_unit, c.status, c.operations,
		       a.type, COALESCE(ab.balance, 0)
		FROM currencies c
		LEFT JOIN accounts a
		  ON a.owner_type = 'user' AND a.owner_id = $1
		 AND a.currency = c.code AND a.type IN ('cash', 'hold', 'pending', 'frozen')
		 AND a.pocket_code IS NULL AND a.status = 'active'
		LEFT JOIN account_balances ab ON ab.account_id = a.id
		WHERE c.status <> 'draft'
		ORDER BY c.code, a.type`, userID)
	if err != nil {
		return nil, fmt.Errorf("list currency balances: %w", err)
	}
	defer rows.Close()

	byCurrency := make(map[string]*model.CurrencyBalance)
	order := make([]string, 0)
	for rows.Next() {
		var code, status string
		var minorUnit int16
		var rawOperations []byte
		var accountType sql.NullString
		var balance int64
		if err := rows.Scan(&code, &minorUnit, &status, &rawOperations, &accountType, &balance); err != nil {
			return nil, fmt.Errorf("scan currency balance: %w", err)
		}
		code = strings.TrimSpace(code)
		item, ok := byCurrency[code]
		if !ok {
			operations, err := decodeOperations(rawOperations)
			if err != nil {
				return nil, fmt.Errorf("decode currency %s operations: %w", code, err)
			}
			item = &model.CurrencyBalance{
				Currency: code, MinorUnit: minorUnit, Status: status,
				Operations: operations,
			}
			byCurrency[code] = item
			order = append(order, code)
		}
		if !accountType.Valid {
			continue
		}
		switch accountType.String {
		case "cash":
			item.UserEnabled = true
			item.Available = balance
		case "hold":
			item.Hold = balance
		case "pending":
			item.Pending = balance
		case "frozen":
			item.Frozen = balance
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate currency balances: %w", err)
	}

	result := make([]model.CurrencyBalance, 0, len(order))
	for _, code := range order {
		result = append(result, *byCurrency[code])
	}
	return result, nil
}

func (s *Service) GetBalance(ctx context.Context, userID uuid.UUID, code string) (model.CurrencyBalance, error) {
	code, err := canonicalCode(code)
	if err != nil {
		return model.CurrencyBalance{}, err
	}
	balances, err := s.ListBalances(ctx, userID)
	if err != nil {
		return model.CurrencyBalance{}, err
	}
	for _, balance := range balances {
		if balance.Currency == code {
			if !balance.UserEnabled {
				return model.CurrencyBalance{}, fmt.Errorf("%w: user %s has no %s account", apperror.ErrCurrencyAccountMissing, userID, code)
			}
			return balance, nil
		}
	}
	return model.CurrencyBalance{}, fmt.Errorf("%w: %s", apperror.ErrCurrencyInvalid, code)
}

func (s *Service) ListPairs(ctx context.Context) ([]model.FXPair, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.pair_code, p.base_currency, p.quote_currency, p.status, p.rate_source,
		       p.rate_convention, p.position_qualifier, p.pair_policy_version, p.quote_ttl_seconds,
		       p.rounding_mode, d.id, d.source_currency, d.target_currency,
		       d.enabled, d.new_quotes_paused, d.conversions_paused,
		       d.min_source_amount, d.max_source_amount, d.spread_basis_points
		FROM fx_pairs p
		JOIN fx_pair_directions d ON d.pair_id = p.id
		ORDER BY p.base_currency, p.quote_currency, d.source_currency`)
	if err != nil {
		return nil, fmt.Errorf("list FX pairs: %w", err)
	}
	defer rows.Close()

	byID := make(map[uuid.UUID]*model.FXPair)
	order := make([]uuid.UUID, 0)
	for rows.Next() {
		var pair model.FXPair
		var direction model.FXDirection
		if err := rows.Scan(
			&pair.ID, &pair.PairCode, &pair.BaseCurrency, &pair.QuoteCurrency, &pair.Status, &pair.RateSource,
			&pair.RateConvention, &pair.PositionQualifier, &pair.PairPolicyVersion, &pair.QuoteTTLSeconds,
			&pair.RoundingMode, &direction.ID, &direction.SourceCurrency,
			&direction.TargetCurrency, &direction.Enabled, &direction.NewQuotesPaused,
			&direction.ConversionsPaused, &direction.MinSourceAmount,
			&direction.MaxSourceAmount, &direction.SpreadBasisPoints,
		); err != nil {
			return nil, fmt.Errorf("scan FX pair: %w", err)
		}
		pair.PairCode = strings.TrimSpace(pair.PairCode)
		pair.BaseCurrency = strings.TrimSpace(pair.BaseCurrency)
		pair.QuoteCurrency = strings.TrimSpace(pair.QuoteCurrency)
		pair.RateConvention = strings.TrimSpace(pair.RateConvention)
		direction.SourceCurrency = strings.TrimSpace(direction.SourceCurrency)
		direction.TargetCurrency = strings.TrimSpace(direction.TargetCurrency)
		direction.PairID = pair.ID
		current, ok := byID[pair.ID]
		if !ok {
			pair.Directions = []model.FXDirection{direction}
			byID[pair.ID] = &pair
			order = append(order, pair.ID)
			continue
		}
		current.Directions = append(current.Directions, direction)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate FX pairs: %w", err)
	}
	result := make([]model.FXPair, 0, len(order))
	for _, id := range order {
		result = append(result, *byID[id])
	}
	return result, nil
}

func (s *Service) CreateQuote(ctx context.Context, userID uuid.UUID, sourceCode, targetCode string, sourceAmount int64, requestKey string) (model.FXQuote, error) {
	started := time.Now()
	result, err := s.createQuote(ctx, userID, sourceCode, targetCode, sourceAmount, requestKey)
	observeFXQuote(sourceCode, targetCode, started, err)
	return result, err
}

func (s *Service) createQuote(ctx context.Context, userID uuid.UUID, sourceCode, targetCode string, sourceAmount int64, requestKey string) (model.FXQuote, error) {
	if userID == uuid.Nil {
		return model.FXQuote{}, fmt.Errorf("%w: user id is required", apperror.ErrValidation)
	}
	if sourceAmount <= 0 {
		return model.FXQuote{}, fmt.Errorf("%w: source amount must be positive", apperror.ErrValidation)
	}
	requestKey, err := validateKey(requestKey)
	if err != nil {
		return model.FXQuote{}, err
	}
	sourceCode, err = canonicalCode(sourceCode)
	if err != nil {
		return model.FXQuote{}, err
	}
	targetCode, err = canonicalCode(targetCode)
	if err != nil {
		return model.FXQuote{}, err
	}
	if sourceCode == targetCode {
		return model.FXQuote{}, fmt.Errorf("%w: source and target currency must differ", apperror.ErrValidation)
	}

	var result model.FXQuote
	now := s.now()
	err = s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if err := lockFXIdempotencyKey(ctx, tx, "quote", userID, requestKey); err != nil {
			return err
		}
		var existing model.FXQuote
		lookupErr := scanFXQuote(tx.QueryRowContext(ctx,
			quoteSelect+` WHERE user_id = $1 AND request_key = $2 FOR UPDATE`, userID, requestKey), &existing)
		if lookupErr == nil {
			if existing.SourceCurrency != sourceCode || existing.TargetCurrency != targetCode || existing.SourceAmount != sourceAmount {
				return apperror.ErrFXQuoteMismatch
			}
			result = existing
			return nil
		}
		if !errors.Is(lookupErr, sql.ErrNoRows) {
			return fmt.Errorf("lookup FX quote idempotency key: %w", lookupErr)
		}

		sourcePolicy, err := loadCurrencyPolicy(ctx, tx, sourceCode)
		if err != nil {
			return err
		}
		targetPolicy, err := loadCurrencyPolicy(ctx, tx, targetCode)
		if err != nil {
			return err
		}
		if err := requireFXCurrency(sourcePolicy, "fx_source"); err != nil {
			return err
		}
		if err := requireFXCurrency(targetPolicy, "fx_target"); err != nil {
			return err
		}
		if err := requireUserCashAccounts(ctx, tx, userID, sourceCode, targetCode); err != nil {
			return err
		}

		var pairID, directionID, rateVersionID uuid.UUID
		var ttlSeconds int
		var baseCurrency, quoteCurrency, rateConvention, roundingMode, referenceRateRaw string
		var pairPolicyVersion int64
		var minAmount, maxAmount, spread int64
		lookupRateErr := tx.QueryRowContext(ctx, `
			SELECT p.id, p.base_currency, p.quote_currency, p.rate_convention,
			       p.pair_policy_version, d.id, r.id, p.quote_ttl_seconds, p.rounding_mode,
			       d.min_source_amount, d.max_source_amount, d.spread_basis_points,
			       r.reference_rate::text
			FROM fx_pairs p
			JOIN fx_pair_directions d ON d.pair_id = p.id
			JOIN fx_rate_versions r ON r.direction_id = d.id
			WHERE p.status = 'active'
			  AND d.source_currency = $1 AND d.target_currency = $2
			  AND d.enabled = true AND d.new_quotes_paused = false
				AND r.status = 'active' AND r.effective_from <= $3
			  AND (r.effective_to IS NULL OR r.effective_to > $3)
			ORDER BY r.version DESC
			LIMIT 1`, sourceCode, targetCode, now).Scan(
			&pairID, &baseCurrency, &quoteCurrency, &rateConvention, &pairPolicyVersion,
			&directionID, &rateVersionID, &ttlSeconds, &roundingMode,
			&minAmount, &maxAmount, &spread, &referenceRateRaw,
		)
		if errors.Is(lookupRateErr, sql.ErrNoRows) {
			var pairStatus string
			var directionEnabled, newQuotesPaused, activeRate bool
			policyErr := tx.QueryRowContext(ctx, `
				SELECT p.status, d.enabled, d.new_quotes_paused,
				       EXISTS (
						SELECT 1 FROM fx_rate_versions r
						WHERE r.direction_id = d.id AND r.status = 'active'
						  AND r.effective_from <= $3
						  AND (r.effective_to IS NULL OR r.effective_to > $3)
					   )
				FROM fx_pairs p
				JOIN fx_pair_directions d ON d.pair_id = p.id
				WHERE d.source_currency = $1 AND d.target_currency = $2
				ORDER BY p.id
				LIMIT 1`, sourceCode, targetCode, now).Scan(
				&pairStatus, &directionEnabled, &newQuotesPaused, &activeRate)
			if errors.Is(policyErr, sql.ErrNoRows) {
				return apperror.ErrFXPairUnavailable
			}
			if policyErr != nil {
				return fmt.Errorf("inspect FX quote availability: %w", policyErr)
			}
			if pairStatus != "active" {
				return apperror.ErrFXPairUnavailable
			}
			if !directionEnabled || newQuotesPaused {
				return apperror.ErrFXDirectionDisabled
			}
			if !activeRate {
				return apperror.ErrFXRateUnavailable
			}
		}
		if lookupRateErr != nil {
			return fmt.Errorf("lookup FX rate: %w", lookupRateErr)
		}
		if sourceAmount < minAmount || sourceAmount > maxAmount {
			return fmt.Errorf("%w: source amount outside FX direction limits", apperror.ErrCurrencyLimitExceeded)
		}

		rate, err := currency.ParseRate(referenceRateRaw)
		if err != nil {
			return fmt.Errorf("%w: %v", apperror.ErrFXRateInvalid, err)
		}
		baseCurrency = strings.TrimSpace(baseCurrency)
		quoteCurrency = strings.TrimSpace(quoteCurrency)
		rateConvention = strings.TrimSpace(rateConvention)
		calculationRate, clientRate, err := canonicalDirectionalRates(
			baseCurrency, quoteCurrency, sourceCode, targetCode, rate, spread,
		)
		if err != nil {
			return err
		}
		clientRateText, err := clientRate.ExactString()
		if err != nil {
			return fmt.Errorf("%w: client rate is invalid: %v", apperror.ErrFXRateInvalid, err)
		}
		sourceMoney := currency.Money{Currency: sourceCode, Minor: sourceAmount}
		targetMoney, remainder, err := currency.ConvertWithMinorUnits(
			sourceMoney, targetCode, sourcePolicy.MinorUnit, targetPolicy.MinorUnit,
			calculationRate, 0,
		)
		if err != nil {
			if errors.Is(err, currency.ErrZeroTarget) {
				return apperror.ErrFXTargetAmountZero
			}
			if errors.Is(err, currency.ErrOverflow) {
				return apperror.ErrMoneyOverflow
			}
			return fmt.Errorf("convert FX quote: %w", err)
		}
		quoteID := uuid.New()
		expiresAt := now.Add(time.Duration(ttlSeconds) * time.Second)
		var createdAt time.Time
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO fx_quotes (
				id, user_id, pair_id, direction_id, rate_version_id,
				source_currency, target_currency, source_minor_unit, target_minor_unit,
				source_amount, target_amount, reference_rate, client_rate,
				rate_convention, pair_policy_version, spread_basis_points, rounding_mode,
				rounding_remainder_numerator, rounding_remainder_denominator,
				request_key, status, expires_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
			          $13, $14, $15, $16, $17, $18, $19, $20, 'active', $21)
			RETURNING created_at`,
			quoteID, userID, pairID, directionID, rateVersionID,
			sourceCode, targetCode, sourcePolicy.MinorUnit, targetPolicy.MinorUnit,
			sourceAmount, targetMoney.Minor, referenceRateRaw, clientRateText,
			rateConvention, pairPolicyVersion, spread, roundingMode,
			remainder.Numerator, remainder.Denominator, requestKey, expiresAt,
		).Scan(&createdAt); err != nil {
			return fmt.Errorf("insert FX quote: %w", err)
		}
		result = model.FXQuote{
			ID: quoteID, UserID: userID, PairID: pairID, DirectionID: directionID,
			RateVersionID: rateVersionID, SourceCurrency: sourceCode,
			TargetCurrency: targetCode, SourceMinorUnit: sourcePolicy.MinorUnit,
			TargetMinorUnit: targetPolicy.MinorUnit, SourceAmount: sourceAmount,
			TargetAmount: targetMoney.Minor, ReferenceRate: referenceRateRaw,
			ClientRate: clientRateText, RateConvention: rateConvention,
			PairPolicyVersion: pairPolicyVersion, SpreadBasisPoints: spread,
			RoundingMode:      roundingMode,
			RoundingRemainder: remainder.Numerator + "/" + remainder.Denominator,
			RequestKey:        requestKey, Status: "active", ExpiresAt: expiresAt,
			CreatedAt: createdAt,
		}
		return nil
	})
	if err != nil {
		return model.FXQuote{}, err
	}
	return result, nil
}

func (s *Service) GetQuote(ctx context.Context, userID, quoteID uuid.UUID) (model.FXQuote, error) {
	if userID == uuid.Nil || quoteID == uuid.Nil {
		return model.FXQuote{}, fmt.Errorf("%w: user and quote id are required", apperror.ErrValidation)
	}
	var quote model.FXQuote
	err := scanFXQuote(s.db.QueryRowContext(ctx,
		quoteSelect+` WHERE user_id = $1 AND id = $2`, userID, quoteID), &quote)
	if errors.Is(err, sql.ErrNoRows) {
		return model.FXQuote{}, apperror.ErrFXQuoteNotFound
	}
	if err != nil {
		return model.FXQuote{}, fmt.Errorf("get FX quote: %w", err)
	}
	return quote, nil
}

func (s *Service) GetQuoteForAdmin(ctx context.Context, quoteID uuid.UUID) (model.FXQuote, error) {
	if quoteID == uuid.Nil {
		return model.FXQuote{}, fmt.Errorf("%w: quote id is required", apperror.ErrValidation)
	}
	var quote model.FXQuote
	err := scanFXQuote(s.db.QueryRowContext(ctx,
		quoteSelect+` WHERE id = $1`, quoteID), &quote)
	if errors.Is(err, sql.ErrNoRows) {
		return model.FXQuote{}, apperror.ErrFXQuoteNotFound
	}
	if err != nil {
		return model.FXQuote{}, fmt.Errorf("get FX quote for admin: %w", err)
	}
	return quote, nil
}

func (s *Service) ExecuteConversion(ctx context.Context, userID, quoteID uuid.UUID, idempotencyKey string, expectedSource, expectedTarget int64) (model.FXConversion, error) {
	started := time.Now()
	result, err := s.executeConversion(ctx, userID, quoteID, idempotencyKey, expectedSource, expectedTarget)
	if err == nil {
		// Metrics are refreshed after the commit, never used to decide whether
		// the money movement succeeds. A dashboard read failure must not turn a
		// posted conversion into an application error.
		_, _ = s.ListPositions(ctx)
	}
	observeFXConversion(result.SourceCurrency, result.TargetCurrency, started, err)
	return result, err
}

func (s *Service) executeConversion(ctx context.Context, userID, quoteID uuid.UUID, idempotencyKey string, expectedSource, expectedTarget int64) (model.FXConversion, error) {
	if userID == uuid.Nil || quoteID == uuid.Nil {
		return model.FXConversion{}, fmt.Errorf("%w: user and quote id are required", apperror.ErrValidation)
	}
	idempotencyKey, err := validateKey(idempotencyKey)
	if err != nil {
		return model.FXConversion{}, err
	}
	if expectedSource <= 0 || expectedTarget <= 0 {
		return model.FXConversion{}, fmt.Errorf("%w: expected amounts must be positive", apperror.ErrValidation)
	}

	var result model.FXConversion
	now := s.now()
	err = s.db.WithTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable}, func(tx *sql.Tx) error {
		if err := lockFXIdempotencyKey(ctx, tx, "conversion", userID, idempotencyKey); err != nil {
			return err
		}
		var existing model.FXConversion
		lookupErr := scanFXConversion(tx.QueryRowContext(ctx,
			conversionSelect+` WHERE user_id = $1 AND idempotency_key = $2 FOR UPDATE`, userID, idempotencyKey), &existing)
		if lookupErr == nil {
			if existing.QuoteID != quoteID || existing.SourceAmount != expectedSource || existing.TargetAmount != expectedTarget {
				return apperror.ErrIdempotencyConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(lookupErr, sql.ErrNoRows) {
			return fmt.Errorf("lookup FX conversion idempotency key: %w", lookupErr)
		}

		var quote model.FXQuote
		if err := scanFXQuote(tx.QueryRowContext(ctx,
			quoteSelect+` WHERE user_id = $1 AND id = $2 FOR UPDATE`, userID, quoteID), &quote); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return apperror.ErrFXQuoteNotFound
			}
			return fmt.Errorf("lock FX quote: %w", err)
		}
		if quote.Status != "active" {
			if quote.Status == "consumed" {
				return apperror.ErrFXQuoteAlreadyConsumed
			}
			return apperror.ErrFXQuoteExpired
		}
		if !now.Before(quote.ExpiresAt) {
			return apperror.ErrFXQuoteExpired
		}
		if quote.SourceAmount != expectedSource || quote.TargetAmount != expectedTarget {
			return apperror.ErrFXQuoteMismatch
		}

		var directionEnabled, conversionsPaused bool
		if err := tx.QueryRowContext(ctx,
			`SELECT d.enabled, d.conversions_paused
			 FROM fx_pair_directions d
			 JOIN fx_pairs p ON p.id = d.pair_id
			 WHERE d.id = $1 AND p.status = 'active'
			 FOR UPDATE`, quote.DirectionID).Scan(&directionEnabled, &conversionsPaused); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return apperror.ErrFXDirectionDisabled
			}
			return fmt.Errorf("lock FX direction: %w", err)
		}
		if !directionEnabled {
			return apperror.ErrFXDirectionDisabled
		}
		if conversionsPaused {
			return apperror.ErrFXConversionsPaused
		}

		sourcePolicy, err := loadCurrencyPolicy(ctx, tx, quote.SourceCurrency)
		if err != nil {
			return err
		}
		targetPolicy, err := loadCurrencyPolicy(ctx, tx, quote.TargetCurrency)
		if err != nil {
			return err
		}
		if err := requireExistingFXCurrency(sourcePolicy); err != nil {
			return err
		}
		if err := requireExistingFXCurrency(targetPolicy); err != nil {
			return err
		}

		var sourceUserID, targetUserID, sourcePositionID, targetPositionID uuid.UUID
		var positionQualifier string
		accountErr := tx.QueryRowContext(ctx, `
			SELECT su.id, tu.id
			FROM accounts su
			JOIN accounts tu ON tu.owner_type = 'user' AND tu.owner_id = $1
			                 AND tu.type = 'cash' AND tu.currency = $3
			                 AND tu.pocket_code IS NULL AND tu.status = 'active'
			WHERE su.owner_type = 'user' AND su.owner_id = $1
			  AND su.type = 'cash' AND su.currency = $2
			  AND su.pocket_code IS NULL AND su.status = 'active'`,
			userID, quote.SourceCurrency, quote.TargetCurrency).Scan(&sourceUserID, &targetUserID)
		if errors.Is(accountErr, sql.ErrNoRows) {
			return fmt.Errorf("%w: source or target user currency account is missing", apperror.ErrCurrencyAccountMissing)
		}
		if accountErr != nil {
			return fmt.Errorf("resolve FX user accounts: %w", accountErr)
		}
		positionErr := tx.QueryRowContext(ctx, `
			SELECT sp.id, tp.id, p.position_qualifier
			FROM fx_pairs p
			JOIN accounts sp ON sp.owner_type = 'system' AND sp.type = 'fx_conversion'
			                  AND sp.currency = $2 AND sp.system_qualifier = p.position_qualifier
			                  AND sp.status = 'active'
			JOIN accounts tp ON tp.owner_type = 'system' AND tp.type = 'fx_conversion'
			                  AND tp.currency = $3 AND tp.system_qualifier = p.position_qualifier
			                  AND tp.status = 'active'
			WHERE p.id = $1`, quote.PairID, quote.SourceCurrency, quote.TargetCurrency).Scan(
			&sourcePositionID, &targetPositionID, &positionQualifier,
		)
		if errors.Is(positionErr, sql.ErrNoRows) {
			return fmt.Errorf("%w: source or target FX position account is missing", apperror.ErrCurrencySystemAccountMissing)
		}
		if positionErr != nil {
			return fmt.Errorf("resolve FX position accounts: %w", positionErr)
		}

		// Lock the pair's limit rows before any position balance row. Rebalances
		// use the same limit-before-position order, so a conversion and a
		// governed rebalance cannot deadlock while competing at a hard bound.
		// User balances are locked after the pair coordination rows and before
		// position balances; every FX conversion follows this same order.
		if err := lockFXPositionLimits(ctx, tx, quote.PairID); err != nil {
			return err
		}
		userBalances, err := s.balanceRepo.LockBalances(ctx, tx, []uuid.UUID{sourceUserID, targetUserID})
		if err != nil {
			return fmt.Errorf("lock FX user balances: %w", err)
		}
		positionBalances, err := s.balanceRepo.LockBalances(ctx, tx, []uuid.UUID{sourcePositionID, targetPositionID})
		if err != nil {
			return fmt.Errorf("lock FX position balances: %w", err)
		}
		balances := make(map[uuid.UUID]model.AccountBalance, len(userBalances)+len(positionBalances))
		maps.Copy(balances, userBalances)
		maps.Copy(balances, positionBalances)
		if err := validateFXBalances(balances, sourceUserID, targetUserID, sourcePositionID, targetPositionID, quote.SourceCurrency, quote.TargetCurrency); err != nil {
			return err
		}
		sourceUserBalance, err := decimalInt64(balances[sourceUserID].Balance)
		if err != nil {
			return err
		}
		targetUserBalance, err := decimalInt64(balances[targetUserID].Balance)
		if err != nil {
			return err
		}
		sourcePositionBalance, err := decimalInt64(balances[sourcePositionID].Balance)
		if err != nil {
			return err
		}
		targetPositionBalance, err := decimalInt64(balances[targetPositionID].Balance)
		if err != nil {
			return err
		}
		if sourceUserBalance < quote.SourceAmount {
			return apperror.ErrInsufficientFunds
		}
		sourceUserAfter, err := addInt64(sourceUserBalance, -quote.SourceAmount)
		if err != nil {
			return apperror.ErrMoneyOverflow
		}
		targetUserAfter, err := addInt64(targetUserBalance, quote.TargetAmount)
		if err != nil {
			return apperror.ErrMoneyOverflow
		}
		sourcePositionAfter, err := addInt64(sourcePositionBalance, quote.SourceAmount)
		if err != nil {
			return apperror.ErrMoneyOverflow
		}
		targetPositionAfter, err := addInt64(targetPositionBalance, -quote.TargetAmount)
		if err != nil {
			return apperror.ErrMoneyOverflow
		}
		if err := checkPositionLimits(ctx, tx, quote.PairID, map[string]int64{
			quote.SourceCurrency: sourcePositionAfter,
			quote.TargetCurrency: targetPositionAfter,
		}); err != nil {
			return err
		}

		conversionID := generalutil.NewV7()
		sourceTransactionID := generalutil.NewV7()
		targetTransactionID := generalutil.NewV7()
		var createdAt time.Time
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO fx_conversions (
				id, user_id, quote_id, idempotency_key, source_currency,
				target_currency, source_amount, target_amount, status
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending')
			RETURNING created_at`,
			conversionID, userID, quote.ID, idempotencyKey, quote.SourceCurrency,
			quote.TargetCurrency, quote.SourceAmount, quote.TargetAmount,
		).Scan(&createdAt); err != nil {
			return fmt.Errorf("insert pending FX conversion: %w", err)
		}
		scope := "fx"
		sourceKey := "fx:" + conversionID.String() + ":source"
		targetKey := "fx:" + conversionID.String() + ":target"
		if err := s.txRepo.Insert(ctx, tx, repository.InsertTransactionParams{
			ID: sourceTransactionID, IdempotencyKey: sourceKey, IdempotencyScope: &scope,
			Type: "fx_out", Amount: decimal.NewFromInt(quote.SourceAmount), Currency: quote.SourceCurrency,
			SourceAccountID: &sourceUserID, DestinationAccountID: &sourcePositionID,
		}); err != nil {
			return fmt.Errorf("insert FX source transaction: %w", err)
		}
		if err := s.txRepo.Insert(ctx, tx, repository.InsertTransactionParams{
			ID: targetTransactionID, IdempotencyKey: targetKey, IdempotencyScope: &scope,
			Type: "fx_in", Amount: decimal.NewFromInt(quote.TargetAmount), Currency: quote.TargetCurrency,
			SourceAccountID: &targetPositionID, DestinationAccountID: &targetUserID,
		}); err != nil {
			return fmt.Errorf("insert FX target transaction: %w", err)
		}
		if err := linkFXTransaction(ctx, tx, sourceTransactionID, conversionID, quote.ID, "source", targetTransactionID); err != nil {
			return err
		}
		if err := linkFXTransaction(ctx, tx, targetTransactionID, conversionID, quote.ID, "target", sourceTransactionID); err != nil {
			return err
		}

		allNewBalances := map[uuid.UUID]decimal.Decimal{
			sourceUserID:     decimal.NewFromInt(sourceUserAfter),
			sourcePositionID: decimal.NewFromInt(sourcePositionAfter),
			targetPositionID: decimal.NewFromInt(targetPositionAfter),
			targetUserID:     decimal.NewFromInt(targetUserAfter),
		}
		sourceCommand := processors.ResolvedCommand{
			Command: processors.Command{
				IdempotencyKey: sourceKey, IdempotencyScope: scope, Type: "fx_out",
				Amount: decimal.NewFromInt(quote.SourceAmount), UserID: userID, Currency: quote.SourceCurrency,
				Metadata: map[string]any{"quote_id": quote.ID.String(), "rate": quote.ClientRate, "pair": positionQualifier},
			},
			AccountIDs: []uuid.UUID{sourceUserID, sourcePositionID}, Currency: quote.SourceCurrency,
			Source: sourceUserID, Destination: sourcePositionID,
		}
		targetCommand := processors.ResolvedCommand{
			Command: processors.Command{
				IdempotencyKey: targetKey, IdempotencyScope: scope, Type: "fx_in",
				Amount: decimal.NewFromInt(quote.TargetAmount), UserID: userID, Currency: quote.TargetCurrency,
				Metadata: map[string]any{"quote_id": quote.ID.String(), "rate": quote.ClientRate, "pair": positionQualifier},
			},
			AccountIDs: []uuid.UUID{targetPositionID, targetUserID}, Currency: quote.TargetCurrency,
			Source: targetPositionID, Destination: targetUserID,
		}
		sourceProcessor := processors.NewFxOut(nil)
		targetProcessor := processors.NewFxIn(nil)
		sourceEntries, err := sourceProcessor.BuildEntries(ctx, tx, sourceCommand, balances)
		if err != nil {
			return fmt.Errorf("build FX source entries: %w", err)
		}
		targetEntries, err := targetProcessor.BuildEntries(ctx, tx, targetCommand, balances)
		if err != nil {
			return fmt.Errorf("build FX target entries: %w", err)
		}
		if err := s.entryRepo.InsertEntries(ctx, tx, sourceTransactionID, sourceEntries, allNewBalances); err != nil {
			return fmt.Errorf("insert FX source entries: %w", err)
		}
		if err := s.entryRepo.InsertEntries(ctx, tx, targetTransactionID, targetEntries, allNewBalances); err != nil {
			return fmt.Errorf("insert FX target entries: %w", err)
		}
		if err := s.balanceRepo.UpdateBalances(ctx, tx, allNewBalances); err != nil {
			return fmt.Errorf("update FX balances: %w", err)
		}
		if err := s.txRepo.UpdateStatus(ctx, tx, sourceTransactionID, "posted", nil); err != nil {
			return fmt.Errorf("post FX source transaction: %w", err)
		}
		if err := s.txRepo.UpdateStatus(ctx, tx, targetTransactionID, "posted", nil); err != nil {
			return fmt.Errorf("post FX target transaction: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE fx_conversions
			SET status = 'posted', source_transaction_id = $1,
			    target_transaction_id = $2, posted_at = $3
			WHERE id = $4`, sourceTransactionID, targetTransactionID, now, conversionID); err != nil {
			return fmt.Errorf("post FX conversion: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE fx_quotes
			SET status = 'consumed', consumed_at = $1, consumed_by_conversion_id = $2
			WHERE id = $3`, now, conversionID, quote.ID); err != nil {
			return fmt.Errorf("consume FX quote: %w", err)
		}
		events := append(sourceProcessor.OutboxEvents(sourceCommand, sourceTransactionID, sourceEntries), targetProcessor.OutboxEvents(targetCommand, targetTransactionID, targetEntries)...)
		events = append(events, fxAggregateOutboxEvent(quote, conversionID, sourceTransactionID, targetTransactionID, now))
		if err := s.outboxRepo.InsertEvents(ctx, tx, events); err != nil {
			return fmt.Errorf("insert FX outbox events: %w", err)
		}
		result = model.FXConversion{
			ID: conversionID, UserID: userID, QuoteID: quote.ID,
			IdempotencyKey: idempotencyKey, SourceCurrency: quote.SourceCurrency,
			TargetCurrency: quote.TargetCurrency, SourceAmount: quote.SourceAmount,
			TargetAmount: quote.TargetAmount, Status: "posted",
			SourceTransactionID: sourceTransactionID, TargetTransactionID: targetTransactionID,
			CreatedAt: createdAt, PostedAt: &now,
		}
		return nil
	})
	if err != nil {
		return model.FXConversion{}, err
	}
	return result, nil
}

func (s *Service) GetConversion(ctx context.Context, userID, conversionID uuid.UUID) (model.FXConversion, error) {
	if userID == uuid.Nil || conversionID == uuid.Nil {
		return model.FXConversion{}, fmt.Errorf("%w: user and conversion id are required", apperror.ErrValidation)
	}
	var result model.FXConversion
	err := scanFXConversion(s.db.QueryRowContext(ctx,
		conversionSelect+` WHERE user_id = $1 AND id = $2`, userID, conversionID), &result)
	if errors.Is(err, sql.ErrNoRows) {
		return model.FXConversion{}, apperror.ErrFXConversionNotFound
	}
	if err != nil {
		return model.FXConversion{}, fmt.Errorf("get FX conversion: %w", err)
	}
	return result, nil
}

func (s *Service) GetConversionForAdmin(ctx context.Context, conversionID uuid.UUID) (model.FXConversion, error) {
	if conversionID == uuid.Nil {
		return model.FXConversion{}, fmt.Errorf("%w: conversion id is required", apperror.ErrValidation)
	}
	var result model.FXConversion
	err := scanFXConversion(s.db.QueryRowContext(ctx,
		conversionSelect+` WHERE id = $1`, conversionID), &result)
	if errors.Is(err, sql.ErrNoRows) {
		return model.FXConversion{}, apperror.ErrFXConversionNotFound
	}
	if err != nil {
		return model.FXConversion{}, fmt.Errorf("get FX conversion for admin: %w", err)
	}
	return result, nil
}

const quoteSelect = `
	SELECT id, user_id, pair_id, direction_id, rate_version_id,
	       source_currency, target_currency, source_minor_unit, target_minor_unit,
	       source_amount, target_amount, reference_rate::text, client_rate::text,
	       rate_convention, pair_policy_version, spread_basis_points,
	       rounding_mode,
	       rounding_remainder_numerator::text, rounding_remainder_denominator::text,
	       request_key,
	       CASE WHEN status = 'active' AND expires_at <= now() THEN 'expired' ELSE status END,
	       expires_at, consumed_at,
	       consumed_by_conversion_id, created_at
	FROM fx_quotes`

func scanFXQuote(scanner rowScanner, result *model.FXQuote) error {
	var sourceCurrency, targetCurrency string
	var remainderNumerator, remainderDenominator string
	var consumedAt sql.NullTime
	var consumedBy uuid.NullUUID
	if err := scanner.Scan(
		&result.ID, &result.UserID, &result.PairID, &result.DirectionID, &result.RateVersionID,
		&sourceCurrency, &targetCurrency, &result.SourceMinorUnit, &result.TargetMinorUnit,
		&result.SourceAmount, &result.TargetAmount, &result.ReferenceRate, &result.ClientRate,
		&result.RateConvention, &result.PairPolicyVersion, &result.SpreadBasisPoints,
		&result.RoundingMode, &remainderNumerator, &remainderDenominator,
		&result.RequestKey, &result.Status, &result.ExpiresAt, &consumedAt,
		&consumedBy, &result.CreatedAt,
	); err != nil {
		return err
	}
	result.SourceCurrency = strings.TrimSpace(sourceCurrency)
	result.TargetCurrency = strings.TrimSpace(targetCurrency)
	result.RoundingRemainder = remainderNumerator + "/" + remainderDenominator
	if consumedAt.Valid {
		result.ConsumedAt = &consumedAt.Time
	}
	if consumedBy.Valid {
		result.ConsumedByConversion = consumedBy.UUID
	}
	return nil
}

const conversionSelect = `
	SELECT id, user_id, quote_id, idempotency_key, source_currency,
	       target_currency, source_amount, target_amount, status,
	       source_transaction_id, target_transaction_id, error_message,
	       created_at, posted_at
	FROM fx_conversions`

func scanFXConversion(scanner rowScanner, result *model.FXConversion) error {
	var sourceCurrency, targetCurrency string
	var sourceTransaction, targetTransaction uuid.NullUUID
	var errorMessage sql.NullString
	var postedAt sql.NullTime
	if err := scanner.Scan(
		&result.ID, &result.UserID, &result.QuoteID, &result.IdempotencyKey,
		&sourceCurrency, &targetCurrency, &result.SourceAmount, &result.TargetAmount,
		&result.Status, &sourceTransaction, &targetTransaction, &errorMessage,
		&result.CreatedAt, &postedAt,
	); err != nil {
		return err
	}
	result.SourceCurrency = strings.TrimSpace(sourceCurrency)
	result.TargetCurrency = strings.TrimSpace(targetCurrency)
	if sourceTransaction.Valid {
		result.SourceTransactionID = sourceTransaction.UUID
	}
	if targetTransaction.Valid {
		result.TargetTransactionID = targetTransaction.UUID
	}
	if errorMessage.Valid {
		result.ErrorMessage = errorMessage.String
	}
	if postedAt.Valid {
		result.PostedAt = &postedAt.Time
	}
	return nil
}

func loadCurrencyPolicy(ctx context.Context, q queryer, code string) (currencyPolicy, error) {
	var policy currencyPolicy
	var rawOperations []byte
	err := q.QueryRowContext(ctx, `
		SELECT code, minor_unit, status, enabled, operations
		FROM currencies WHERE code = $1`, code).Scan(
		&policy.Code, &policy.MinorUnit, &policy.Status, &policy.Enabled, &rawOperations)
	if errors.Is(err, sql.ErrNoRows) {
		return currencyPolicy{}, fmt.Errorf("%w: %s", apperror.ErrCurrencyInvalid, code)
	}
	if err != nil {
		return currencyPolicy{}, fmt.Errorf("load currency policy: %w", err)
	}
	policy.Code = strings.TrimSpace(policy.Code)
	policy.Operations, err = decodeOperations(rawOperations)
	if err != nil {
		return currencyPolicy{}, fmt.Errorf("decode currency policy: %w", err)
	}
	return policy, nil
}

func requireFXCurrency(policy currencyPolicy, operation string) error {
	if !policy.Enabled || policy.Status != "active" {
		return fmt.Errorf("%w: %s status=%s", apperror.ErrCurrencyDisabled, policy.Code, policy.Status)
	}
	if !policy.Operations[operation] {
		return fmt.Errorf("%w: %s operation=%s", apperror.ErrCurrencyOperationDisabled, policy.Code, operation)
	}
	return nil
}

// requireExistingFXCurrency is used after a quote has been issued. Currency
// lifecycle changes stop new intake, but an already active quote must remain
// finishable unless the pair's explicit conversion pause is engaged.
func requireExistingFXCurrency(policy currencyPolicy) error {
	switch policy.Status {
	case "active", "intake_paused", "disabled":
		return nil
	default:
		return fmt.Errorf("%w: %s status=%s", apperror.ErrCurrencyDisabled, policy.Code, policy.Status)
	}
}

func requireUserCashAccounts(ctx context.Context, tx *sql.Tx, userID uuid.UUID, source, target string) error {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM accounts
		WHERE owner_type = 'user' AND owner_id = $1 AND type = 'cash'
		  AND currency IN ($2, $3) AND pocket_code IS NULL AND status = 'active'`,
		userID, source, target).Scan(&count); err != nil {
		return fmt.Errorf("check user FX accounts: %w", err)
	}
	if count != 2 {
		return fmt.Errorf("%w: user %s needs active cash accounts in %s and %s", apperror.ErrCurrencyAccountMissing, userID, source, target)
	}
	return nil
}

func validateFXBalances(balances map[uuid.UUID]model.AccountBalance, sourceUserID, targetUserID, sourcePositionID, targetPositionID uuid.UUID, sourceCurrency, targetCurrency string) error {
	expected := map[uuid.UUID]string{
		sourceUserID:     sourceCurrency,
		targetUserID:     targetCurrency,
		sourcePositionID: sourceCurrency,
		targetPositionID: targetCurrency,
	}
	for accountID, expectedCurrency := range expected {
		balance, ok := balances[accountID]
		if !ok {
			return fmt.Errorf("%w: FX account balance %s is missing", apperror.ErrAccountNotFound, accountID)
		}
		switch balance.Status {
		case constant.AccountStatusActive:
		case constant.AccountStatusSuspended:
			return fmt.Errorf("%w: %s", apperror.ErrAccountSuspended, accountID)
		case constant.AccountStatusClosed:
			return fmt.Errorf("%w: %s", apperror.ErrAccountClosed, accountID)
		default:
			return fmt.Errorf("%w: unknown status %q on account %s", apperror.ErrValidation, balance.Status, accountID)
		}
		if strings.TrimSpace(balance.Currency) != expectedCurrency {
			return fmt.Errorf("%w: account %s has %s, expected %s", apperror.ErrCurrencyMismatch, accountID, balance.Currency, expectedCurrency)
		}
	}
	return nil
}

func decimalInt64(value decimal.Decimal) (int64, error) {
	if !value.Equal(value.Truncate(0)) {
		return 0, fmt.Errorf("%w: balance is not an integer: %s", apperror.ErrMoneyOverflow, value)
	}
	parsed, err := strconv.ParseInt(value.StringFixed(0), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: balance is outside BIGINT range: %s", apperror.ErrMoneyOverflow, value)
	}
	return parsed, nil
}

func checkPositionLimits(ctx context.Context, tx *sql.Tx, pairID uuid.UUID, projected map[string]int64) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT p.pair_code, l.currency, l.minimum_balance, l.maximum_balance
		FROM fx_position_limits l
		JOIN fx_pairs p ON p.id = l.pair_id
		WHERE l.pair_id = $1
		ORDER BY l.currency
		FOR UPDATE`, pairID)
	if err != nil {
		return fmt.Errorf("load FX position limits: %w", err)
	}
	defer rows.Close()

	type bound struct{ minimum, maximum int64 }
	limits := make(map[string]bound, len(projected))
	pairCode := "unknown"
	for rows.Next() {
		var code, currentPairCode string
		var item bound
		if err := rows.Scan(&currentPairCode, &code, &item.minimum, &item.maximum); err != nil {
			return fmt.Errorf("scan FX position limit: %w", err)
		}
		pairCode = strings.TrimSpace(currentPairCode)
		limits[strings.TrimSpace(code)] = item
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate FX position limits: %w", err)
	}

	codes := make([]string, 0, len(projected))
	for code := range projected {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		item, ok := limits[code]
		if !ok {
			observePositionLimitDecision(pairCode, code, "missing")
			return fmt.Errorf("%w: no FX position limit for %s", apperror.ErrFXPositionLimitExceeded, code)
		}
		balance := projected[code]
		if balance < item.minimum || balance > item.maximum {
			observePositionLimitDecision(pairCode, code, "rejected")
			return fmt.Errorf("%w: %s projected balance outside [%d,%d]", apperror.ErrFXPositionLimitExceeded, code, item.minimum, item.maximum)
		}
		observePositionLimitDecision(pairCode, code, "allowed")
	}
	return nil
}

func lockFXPositionLimits(ctx context.Context, tx *sql.Tx, pairID uuid.UUID) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT l.currency, l.minimum_balance, l.maximum_balance
		FROM fx_position_limits l
		WHERE l.pair_id = $1
		ORDER BY l.currency
		FOR UPDATE`, pairID)
	if err != nil {
		return fmt.Errorf("lock FX position limits: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var code string
		var minimum, maximum int64
		if err := rows.Scan(&code, &minimum, &maximum); err != nil {
			return fmt.Errorf("scan FX position limit lock: %w", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate FX position limit locks: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("%w: no FX position limits for pair %s", apperror.ErrFXPositionLimitExceeded, pairID)
	}
	return nil
}

func linkFXTransaction(ctx context.Context, tx *sql.Tx, id, conversionID, quoteID uuid.UUID, leg string, counterpartID uuid.UUID) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE ledger_transactions
		SET conversion_id = $1, fx_quote_id = $2, fx_leg = $3,
		    counterpart_transaction_id = $4
		WHERE id = $5`, conversionID, quoteID, leg, counterpartID, id); err != nil {
		return fmt.Errorf("link FX %s transaction: %w", leg, err)
	}
	return nil
}

func fxAggregateOutboxEvent(quote model.FXQuote, conversionID, sourceTransactionID, targetTransactionID uuid.UUID, postedAt time.Time) model.OutboxEvent {
	payload := events.NewFXConversionPosted(
		conversionID, quote.ID, quote.UserID, sourceTransactionID,
		quote.SourceCurrency, quote.SourceMinorUnit, strconv.FormatInt(quote.SourceAmount, 10),
		targetTransactionID, quote.TargetCurrency, quote.TargetMinorUnit, strconv.FormatInt(quote.TargetAmount, 10),
		quote.PairID, quote.DirectionID, quote.RateVersionID,
		quote.ClientRate, quote.RateConvention, quote.PairPolicyVersion,
		quote.SpreadBasisPoints, quote.RoundingMode, quote.RoundingRemainder,
		postedAt,
	)
	return model.OutboxEvent{
		AggregateType: "fx_conversion",
		AggregateID:   conversionID,
		EventType:     events.TypeFXConversionPosted,
		Payload:       payload.ToPayload(),
	}
}

// canonicalDirectionalRates keeps the stored rate convention stable: the
// canonical USD/IDR rate is always IDR per USD. For USD -> IDR, the bid is
// applied directly. For IDR -> USD, the ask is applied first and then inverted
// for the minor-unit conversion calculation.
func canonicalDirectionalRates(baseCurrency, quoteCurrency, sourceCurrency, targetCurrency string, reference currency.Rate, spread int64) (currency.Rate, currency.Rate, error) {
	if reference.Value == nil || reference.Value.Sign() <= 0 || spread < 0 || spread >= 10000 {
		return currency.Rate{}, currency.Rate{}, fmt.Errorf("%w: invalid reference rate or spread", apperror.ErrFXRateInvalid)
	}
	baseCurrency = strings.TrimSpace(baseCurrency)
	quoteCurrency = strings.TrimSpace(quoteCurrency)
	if baseCurrency == "" || quoteCurrency == "" || baseCurrency == quoteCurrency {
		return currency.Rate{}, currency.Rate{}, fmt.Errorf("%w: invalid FX pair currencies", apperror.ErrFXRateInvalid)
	}
	bidFactor := new(big.Rat).SetFrac(big.NewInt(10000-spread), big.NewInt(10000))
	askFactor := new(big.Rat).SetFrac(big.NewInt(10000+spread), big.NewInt(10000))
	referenceValue := new(big.Rat).Set(reference.Value)
	switch {
	case sourceCurrency == baseCurrency && targetCurrency == quoteCurrency:
		bid := new(big.Rat).Mul(referenceValue, bidFactor)
		return currency.Rate{Value: bid}, currency.Rate{Value: new(big.Rat).Set(bid)}, nil
	case sourceCurrency == quoteCurrency && targetCurrency == baseCurrency:
		ask := new(big.Rat).Mul(referenceValue, askFactor)
		calculation := new(big.Rat).Inv(new(big.Rat).Set(ask))
		return currency.Rate{Value: calculation}, currency.Rate{Value: ask}, nil
	default:
		return currency.Rate{}, currency.Rate{}, fmt.Errorf("%w: direction %s/%s is not supported by pair %s/%s", apperror.ErrFXRateInvalid, sourceCurrency, targetCurrency, baseCurrency, quoteCurrency)
	}
}

func addInt64(left, right int64) (int64, error) {
	maxInt64 := int64(^uint64(0) >> 1)
	minInt64 := -maxInt64 - 1
	if (right > 0 && left > maxInt64-right) || (right < 0 && left < minInt64-right) {
		return 0, apperror.ErrMoneyOverflow
	}
	return left + right, nil
}

func canonicalCode(code string) (string, error) {
	if len(code) != 3 || code != strings.ToUpper(code) {
		return "", fmt.Errorf("%w: currency must be exactly three uppercase letters", apperror.ErrCurrencyInvalid)
	}
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return "", fmt.Errorf("%w: currency must be exactly three uppercase letters", apperror.ErrCurrencyInvalid)
		}
	}
	return code, nil
}

func validateKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", apperror.ErrEmptyIdempotencyKey
	}
	if len(key) > 255 {
		return "", fmt.Errorf("%w: idempotency key is too long", apperror.ErrValidation)
	}
	return key, nil
}

func lockFXIdempotencyKey(ctx context.Context, tx *sql.Tx, kind string, userID uuid.UUID, key string) error {
	lockKey := "fx:" + kind + ":" + userID.String() + ":" + key
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return fmt.Errorf("lock FX %s idempotency key: %w", kind, err)
	}
	return nil
}

func decodeOperations(raw []byte) (map[string]bool, error) {
	if len(raw) == 0 {
		return cloneOperations(defaultOperations), nil
	}
	var operations map[string]bool
	if err := json.Unmarshal(raw, &operations); err != nil {
		return nil, err
	}
	if operations == nil {
		return cloneOperations(defaultOperations), nil
	}
	return operations, nil
}

func (s *Service) refreshCurrencyRegistry(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT code, minor_unit, status, operations
		FROM currencies
		WHERE status <> 'draft'
		ORDER BY code`)
	if err != nil {
		return fmt.Errorf("list currencies for registry refresh: %w", err)
	}
	defer rows.Close()

	list := make([]currency.Currency, 0)
	for rows.Next() {
		var item currency.Currency
		var rawOperations []byte
		if err := rows.Scan(&item.Code, &item.MinorUnit, &item.Status, &rawOperations); err != nil {
			return fmt.Errorf("scan currency for registry refresh: %w", err)
		}
		item.Operations, err = decodeOperations(rawOperations)
		if err != nil {
			return fmt.Errorf("decode currency %s for registry refresh: %w", item.Code, err)
		}
		list = append(list, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate currencies for registry refresh: %w", err)
	}
	if len(list) == 0 {
		return errors.New("currency registry is empty after policy update")
	}
	currency.Load(list)
	return nil
}

func cloneOperations(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source))
	maps.Copy(result, source)
	return result
}

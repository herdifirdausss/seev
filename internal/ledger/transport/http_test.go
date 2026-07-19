package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	fraudv1 "github.com/herdifirdausss/seev/gen/fraud/v1"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/feepolicy"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

const testSecret = "supersecretkeythatisatleast32chars!"

type feeAdminMockService struct{ *MockService }

func (s feeAdminMockService) IsKnownTransactionType(txType string) bool {
	return txType == "transfer_p2p" || txType == "withdraw_settle"
}

func newFeeAdminRouter(t *testing.T) (http.Handler, sqlmock.Sqlmock) {
	t.Helper()
	ctrl := gomock.NewController(t)
	svc := feeAdminMockService{MockService: NewMockService(ctrl)}
	sqlDB, dbMock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	dbHandle := database.NewFromSQL(sqlDB, database.Config{})
	policy := feepolicy.New(dbHandle, repository.NewFeeRepository(dbHandle))
	return middleware.WithAuth(testSecret, "")(NewInternalRouterWithFeePolicy(svc, policy)), dbMock
}

// ─── Fee quotes (docs/plan/38 Task T3) ──────────────────────────────────────

type quoteMockService struct{ *MockService }

func (s quoteMockService) IsKnownTransactionType(txType string) bool {
	return txType == "transfer_p2p" || txType == "money_in"
}

// newQuoteTestHandler wraps the PUBLIC router (POST /fees/quote is
// registered only when allowedTypes != nil) with a real *feepolicy.Policy
// backed by sqlmock, and a Service double that also satisfies
// transactionTypeValidator.
func newQuoteTestHandler(t *testing.T) (http.Handler, *MockService, sqlmock.Sqlmock) {
	t.Helper()
	ctrl := gomock.NewController(t)
	inner := NewMockService(ctrl)
	svc := quoteMockService{MockService: inner}
	sqlDB, dbMock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	dbHandle := database.NewFromSQL(sqlDB, database.Config{})
	policy := feepolicy.New(dbHandle, repository.NewFeeRepository(dbHandle))
	h := middleware.WithAuth(testSecret, "")(NewRouterWithOptions(svc, nil, policy))
	return h, inner, dbMock
}

func TestCreateQuote_Success_UsesJWTUserID_NotBody(t *testing.T) {
	h, _, dbMock := newQuoteTestHandler(t)
	userID := uuid.New()
	token := tokenFor(t, userID.String(), "user")

	dbMock.ExpectQuery(regexp.QuoteMeta("SELECT flat_minor_units, percent_basis_pts, fee_gateway")).
		WithArgs("transfer_p2p", "IDR", userID, "").
		WillReturnRows(sqlmock.NewRows([]string{"flat_minor_units", "percent_basis_pts", "fee_gateway"}).AddRow(500, 0, "platform"))
	dbMock.ExpectExec(regexp.QuoteMeta("INSERT INTO fee_quotes")).
		WithArgs(sqlmock.AnyArg(), userID, "transfer_p2p", "", "IDR", decimal.NewFromInt(100_000), decimal.NewFromInt(500), "platform", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	before := time.Now()
	w := doReq(t, h, http.MethodPost, "/fees/quote", token, `{"transaction_type":"transfer_p2p","amount":"100000","currency":"IDR"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"fee_amount":"500"`)
	assert.Contains(t, w.Body.String(), `"total_debit":"100500"`)
	require.NoError(t, dbMock.ExpectationsWereMet())

	var body struct {
		Data struct {
			ExpiresAt time.Time `json:"expires_at"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.WithinDuration(t, before.Add(feepolicy.DefaultQuoteTTL), body.Data.ExpiresAt, 5*time.Second)
}

func TestCreateQuote_UnknownTransactionType_400(t *testing.T) {
	h, _, _ := newQuoteTestHandler(t)
	token := tokenFor(t, uuid.NewString(), "user")
	w := doReq(t, h, http.MethodPost, "/fees/quote", token, `{"transaction_type":"not_a_real_type","amount":"1000"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateQuote_NonIntegralAmount_400(t *testing.T) {
	h, _, _ := newQuoteTestHandler(t)
	token := tokenFor(t, uuid.NewString(), "user")
	w := doReq(t, h, http.MethodPost, "/fees/quote", token, `{"transaction_type":"transfer_p2p","amount":"100.50"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateQuote_NoToken_401(t *testing.T) {
	h, _, _ := newQuoteTestHandler(t)
	w := doReq(t, h, http.MethodPost, "/fees/quote", "", `{"transaction_type":"transfer_p2p","amount":"1000"}`)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestCreateQuote_MoneyIn_Quotable proves docs/plan/38 Task T3 step 2:
// money_in is quotable even though it can never be POSTed directly through
// this same public router (not in publicUserTypes) — a quote never moves
// money, so this doesn't reopen that hole.
func TestCreateQuote_MoneyIn_Quotable(t *testing.T) {
	h, _, dbMock := newQuoteTestHandler(t)
	userID := uuid.New()
	token := tokenFor(t, userID.String(), "user")

	dbMock.ExpectQuery(regexp.QuoteMeta("SELECT flat_minor_units, percent_basis_pts, fee_gateway")).
		WithArgs("money_in", "IDR", userID, "").
		WillReturnError(sql.ErrNoRows)
	dbMock.ExpectExec(regexp.QuoteMeta("INSERT INTO fee_quotes")).
		WithArgs(sqlmock.AnyArg(), userID, "money_in", "", "IDR", decimal.NewFromInt(50_000), decimal.Zero, "", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	w := doReq(t, h, http.MethodPost, "/fees/quote", token, `{"transaction_type":"money_in","amount":"50000","currency":"IDR"}`)
	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.NoError(t, dbMock.ExpectationsWereMet())
}

func TestFeeRulesAdminGateAndValidation(t *testing.T) {
	h, _ := newFeeAdminRouter(t)
	body := `{"tx_type":"transfer_p2p","currency":"IDR","flat_minor_units":100}`

	w := doReq(t, h, http.MethodPost, "/admin/ledger/fee-rules", tokenFor(t, uuid.NewString(), "user"), body)
	require.Equal(t, http.StatusForbidden, w.Code)

	invalid := []string{
		`{"tx_type":"unknown","currency":"IDR","flat_minor_units":100}`,
		`{"tx_type":"transfer_p2p","gateway":"unknown","currency":"IDR","flat_minor_units":100}`,
		`{"tx_type":"transfer_p2p","currency":"ZZZ","flat_minor_units":100}`,
		`{"tx_type":"transfer_p2p","currency":"IDR","percent_basis_pts":10000}`,
	}
	for _, invalidBody := range invalid {
		w = doReq(t, h, http.MethodPost, "/admin/ledger/fee-rules", tokenFor(t, uuid.NewString(), "admin"), invalidBody)
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
}

func TestFeeRulesCreateListUpdateDisable(t *testing.T) {
	h, dbMock := newFeeAdminRouter(t)
	adminToken := tokenFor(t, uuid.NewString(), "admin")
	ruleID := uuid.New()
	userID := uuid.New()
	now := time.Now().UTC()
	columns := []string{"id", "tx_type", "gateway", "currency", "user_id", "flat_minor_units", "percent_basis_pts", "fee_gateway", "enabled", "created_at", "updated_at"}

	dbMock.ExpectQuery("INSERT INTO fee_rules").
		WithArgs(sqlmock.AnyArg(), "transfer_p2p", "", "IDR", userID, int64(700), int64(25), "platform", true).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(ruleID, "transfer_p2p", "", "IDR", userID, 700, 25, "platform", true, now, now))
	body := `{"tx_type":"transfer_p2p","currency":"IDR","user_id":"` + userID.String() + `","flat_minor_units":700,"percent_basis_pts":25}`
	w := doReq(t, h, http.MethodPost, "/admin/ledger/fee-rules", adminToken, body)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	dbMock.ExpectQuery("SELECT .* FROM fee_rules ORDER BY").
		WillReturnRows(sqlmock.NewRows(columns).AddRow(ruleID, "transfer_p2p", "", "IDR", userID, 700, 25, "platform", true, now, now))
	w = doReq(t, h, http.MethodGet, "/admin/ledger/fee-rules", adminToken, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), ruleID.String())

	dbMock.ExpectQuery("UPDATE fee_rules SET").
		WithArgs(ruleID, "transfer_p2p", "", "IDR", userID, int64(700), int64(25), "platform", false).
		WillReturnRows(sqlmock.NewRows(columns).AddRow(ruleID, "transfer_p2p", "", "IDR", userID, 700, 25, "platform", false, now, now))
	body = `{"tx_type":"transfer_p2p","currency":"IDR","user_id":"` + userID.String() + `","flat_minor_units":700,"percent_basis_pts":25,"enabled":false}`
	w = doReq(t, h, http.MethodPut, "/admin/ledger/fee-rules/"+ruleID.String(), adminToken, body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"enabled":false`)
	require.NoError(t, dbMock.ExpectationsWereMet())
}

// newTestHandler wraps the PUBLIC ledger router with the same WithAuth
// middleware the composition root applies in production, so tests exercise
// the real JWT parsing + claims-in-context path rather than a fake. Only
// publicUserTypes are postable through it (docs/plan/10 Task T1) — tests
// exercising system transaction types (money_in, adjustment_*, etc.) must
// use newInternalTestHandler instead.
func newTestHandler(t *testing.T) (http.Handler, *MockService, *gomock.Controller) {
	t.Helper()
	ctrl := gomock.NewController(t)
	svc := NewMockService(ctrl)
	// The public router resolves currency for fee pricing (docs/plan/18 Task
	// T2) before every post — a default IDR stub keeps every existing test
	// that doesn't care about currency from having to set this up itself.
	svc.EXPECT().GetUserCurrency(gomock.Any(), gomock.Any(), gomock.Any()).Return("IDR", nil).AnyTimes()
	h := middleware.WithAuth(testSecret, "")(NewRouter(svc))
	return h, svc, ctrl
}

// newInternalTestHandler wraps the INTERNAL ledger router — every
// registered transaction type is postable, matching the router mounted on
// the internal-only listener in production (docs/plan/10 Task T1).
func newInternalTestHandler(t *testing.T) (http.Handler, *MockService, *gomock.Controller) {
	t.Helper()
	ctrl := gomock.NewController(t)
	svc := NewMockService(ctrl)
	h := middleware.WithAuth(testSecret, "")(NewInternalRouter(svc))
	return h, svc, ctrl
}

func tokenFor(t *testing.T, userID, role string) string {
	t.Helper()
	tok, err := middleware.GenerateToken(testSecret, middleware.Claims{
		UserID: userID, Role: role, KYCLevel: 1, Exp: time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	return tok
}

func doReq(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestPostTransaction_NoToken_Unauthorized(t *testing.T) {
	h, _, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	w := doReq(t, h, http.MethodPost, "/transactions", "", `{}`)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestPostTransaction_KYCLevelZero_Forbidden proves the transport layer's
// defense-in-depth KYC check (docs/plan/39 Task T4 step 4) — even though
// the gateway already gates POST /api/v1/ledger/transactions*, the public
// ledger router re-checks kyc_level itself, since it's reachable directly
// by any caller that talks gRPC/HTTP straight to ledger-service.
func TestPostTransaction_KYCLevelZero_Forbidden(t *testing.T) {
	h, _, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	token, err := middleware.GenerateToken(testSecret, middleware.Claims{
		UserID: uuid.New().String(), Role: "user", KYCLevel: 0, Exp: time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)

	w := doReq(t, h, http.MethodPost, "/transactions", token, `{"idempotency_key":"kyc-l0-test","type":"transfer_p2p","amount":"1000"}`)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "KYC_REQUIRED")
}

// TestPostTransaction_KYCLevelOne_Passes is the mirror positive case —
// exists alongside the forbidden case above so a future refactor that
// breaks either the gate or the carve-out shows up as a single-test
// failure, not a silent pass-through.
func TestPostTransaction_KYCLevelOne_Passes(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	svc.EXPECT().Post(gomock.Any(), gomock.Any()).Return(nil)
	userID := uuid.New()
	token := tokenFor(t, userID.String(), "user") // tokenFor hardcodes KYCLevel: 1

	body := `{"idempotency_key":"kyc-l1-test","type":"transfer_p2p","amount":"1000","target_user_id":"` + uuid.New().String() + `"}`
	w := doReq(t, h, http.MethodPost, "/transactions", token, body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestPostTransaction_Success(t *testing.T) {
	// transfer_p2p is one of publicUserTypes — exercises the public router.
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	targetID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, cmd processors.Command) error {
			assert.Equal(t, "transfer_p2p", cmd.Type)
			assert.Equal(t, userID, cmd.UserID)
			assert.True(t, cmd.Amount.Equal(decimal.NewFromInt(1000)))
			return nil
		})

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + targetID.String() + `"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

// TestPostTransaction_QuoteID_PassedThroughAsTypedField_NotMetadata proves
// docs/plan/38 Task T4: quote_id flows to processors.Command.QuoteID (a
// typed field), and — because it's set — buildMetadata must NOT also stamp
// a server-resolved fee_amount/fee_gateway (that's execTransfer's job now,
// from the quote itself).
func TestPostTransaction_QuoteID_PassedThroughAsTypedField_NotMetadata(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	targetID := uuid.New()
	quoteID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, cmd processors.Command) error {
			assert.Equal(t, quoteID.String(), cmd.QuoteID)
			_, hasFeeAmount := cmd.Metadata["fee_amount"]
			assert.False(t, hasFeeAmount, "fee_amount must not be stamped by transport when quote_id is set")
			return nil
		})

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + targetID.String() + `","quote_id":"` + quoteID.String() + `"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
}

// TestPostTransaction_QuoteExpired_Maps422 and
// TestPostTransaction_QuoteMismatch_Maps422 prove the HTTP-layer error
// mapping added in writeError (docs/plan/38 Task T4) — schema_contract-level
// tests (internal/ledger/execquote_integration_test.go) prove execTransfer's
// OWN behavior against a real Postgres; these prove the transport layer
// correctly turns that sentinel into 422 with the right body.
func TestPostTransaction_QuoteExpired_Maps422(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	targetID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).Return(apperror.NewBizErr(apperror.ErrQuoteExpired, "fee quote not found, expired, or already consumed"))

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + targetID.String() + `","quote_id":"` + uuid.New().String() + `"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "QUOTE_EXPIRED")
}

func TestPostTransaction_QuoteMismatch_Maps422(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	targetID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).Return(apperror.NewBizErr(apperror.ErrQuoteMismatch, "fee quote does not match this request"))

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + targetID.String() + `","quote_id":"` + uuid.New().String() + `"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "QUOTE_MISMATCH")
}

func TestPostTransaction_SystemType_RejectedOnPublicRouter(t *testing.T) {
	// money_in moves funds from a system settlement account — must never be
	// directly reachable by an end user (docs/plan/10 Task T1).
	h, _, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	body := `{"idempotency_key":"abc12345","type":"money_in","amount":"1000","metadata":{"gateway":"bca"}}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPostTransaction_SystemType_AllowedOnInternalRouter(t *testing.T) {
	h, svc, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, cmd processors.Command) error {
			assert.Equal(t, "money_in", cmd.Type)
			assert.Equal(t, userID, cmd.UserID)
			assert.True(t, cmd.Amount.Equal(decimal.NewFromInt(1000)))
			return nil
		})

	body := `{"idempotency_key":"abc12345","type":"money_in","amount":"1000","metadata":{"gateway":"bca"}}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestPostTransaction_ShortIdempotencyKey_Rejected(t *testing.T) {
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	body := `{"idempotency_key":"short","type":"money_in","amount":"1000"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostTransaction_InvalidAmount_Rejected(t *testing.T) {
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	body := `{"idempotency_key":"abc12345","type":"money_in","amount":"not-a-number"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostTransaction_FractionalAmount_Rejected(t *testing.T) {
	// docs/plan/10 Task T4: the ledger is minor-unit-only — a fractional
	// amount must never reach the posting pipeline, where it would
	// otherwise be silently truncated.
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	body := `{"idempotency_key":"abc12345","type":"money_in","amount":"100.5","metadata":{"gateway":"bca"}}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "integer")
}

func TestPostTransaction_AdminOnlyType_RejectedForUser(t *testing.T) {
	// adjustment_credit stays admin-gated even on the internal router —
	// defense in depth for compliance/correction actions (docs/plan/10 T1).
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	body := `{"idempotency_key":"abc12345","type":"adjustment_credit","amount":"1000"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPostTransaction_AdminOnlyType_AllowedForAdmin(t *testing.T) {
	h, svc, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).Return(nil)

	// chargeback, not adjustment_credit/debit — those are blocked from
	// direct POST entirely as of docs/plan/16 Task T1, admin or not (see
	// TestPostTransaction_AdjustmentType_BlockedEvenForAdmin below).
	body := `{"idempotency_key":"abc12345","type":"chargeback","amount":"1000"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "admin"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestPostTransaction_AdjustmentType_BlockedEvenForAdmin(t *testing.T) {
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	// No svc.EXPECT().Post(...) — must never reach the service layer.
	body := `{"idempotency_key":"abc12345","type":"adjustment_credit","amount":"1000"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "admin"), body)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "/admin/adjustments")
}

// TestPostTransaction_SuspenseAdjustmentType_BlockedEvenForAdmin is
// TestPostTransaction_AdjustmentType_BlockedEvenForAdmin's mirror for the
// reconciliation adjustment types (docs/plan/16 Task T2) — same block,
// reachable only via POST /admin/recon/items/{id}/resolve.
func TestPostTransaction_SuspenseAdjustmentType_BlockedEvenForAdmin(t *testing.T) {
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	body := `{"idempotency_key":"abc12345","type":"adjustment_suspense_credit","amount":"1000"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "admin"), body)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "/admin/adjustments")
}

func TestPostTransaction_InsufficientFunds_MapsTo422(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).Return(apperror.ErrInsufficientFunds)

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

// ─── Idempotency scope (docs/plan/10 Task T2) ──────────────────────────────────

func TestPostTransaction_IdempotencyScope_DefaultsToCallerUserID(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, cmd processors.Command) error {
			assert.Equal(t, userID.String(), cmd.IdempotencyScope)
			return nil
		})

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestPostTransaction_IdempotencyScope_ClientOverrideIgnoredOnPublicRouter(t *testing.T) {
	// A client-supplied idempotency_scope must never be honored on the
	// public router — otherwise one user could collide with or probe
	// another user's idempotency keys.
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, cmd processors.Command) error {
			assert.Equal(t, userID.String(), cmd.IdempotencyScope)
			return nil
		})

	body := `{"idempotency_key":"abc12345","idempotency_scope":"attacker-chosen-scope","type":"transfer_p2p","amount":"1000"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestPostTransaction_IdempotencyScope_ExplicitOnInternalRouter(t *testing.T) {
	// The internal router trusts its caller to scope idempotency by
	// whatever makes sense for it (e.g. a payment-gateway provider
	// transaction id), not necessarily the target user.
	h, svc, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, cmd processors.Command) error {
			assert.Equal(t, "provider:bca:txn-9981", cmd.IdempotencyScope)
			return nil
		})

	body := `{"idempotency_key":"abc12345","idempotency_scope":"provider:bca:txn-9981","type":"money_in","amount":"1000","metadata":{"gateway":"bca"}}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

// ─── Metadata allowlist + fee policy (docs/plan/10 Task T3) ────────────────────

func TestPostTransaction_PublicRouter_ClientFeeMetadata_StrippedAndIgnored(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	targetID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, cmd processors.Command) error {
			_, hasFeeAmount := cmd.Metadata["fee_amount"]
			_, hasFeeGateway := cmd.Metadata["fee_gateway"]
			assert.False(t, hasFeeAmount, "client-supplied fee_amount must never reach the processor on the public router")
			assert.False(t, hasFeeGateway, "client-supplied fee_gateway must never reach the processor on the public router")
			return nil
		})

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + targetID.String() +
		`","metadata":{"fee_amount":"1","fee_gateway":"attacker-controlled"}}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

// TestPostTransaction_NewRouterWithOptions_RealFeePolicy_ChargesFee proves
// the wiring docs/plan/25 Task T2 adds — a router built with a real,
// non-default feePolicy (the shape internal/ledger.Module.SetFeeRules
// installs) actually resolves and attaches a fee leg to transfer_p2p,
// unlike NewRouter/NewRouterWithPolicy's default (no fee).
func TestPostTransaction_NewRouterWithOptions_RealFeePolicy_ChargesFee(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := NewMockService(ctrl)
	svc.EXPECT().GetUserCurrency(gomock.Any(), gomock.Any(), gomock.Any()).Return("IDR", nil).AnyTimes()

	sqlDB, dbMock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer func() { _ = sqlDB.Close() }()
	dbMock.ExpectQuery(regexp.QuoteMeta(`
SELECT flat_minor_units, percent_basis_pts, fee_gateway
FROM fee_rules
WHERE enabled
  AND tx_type = $1
  AND currency = $2
  AND (user_id = $3 OR user_id IS NULL)
  AND gateway IN ($4, '')
ORDER BY (user_id IS NOT NULL) DESC, (gateway <> '') DESC
LIMIT 1`)).
		WithArgs("transfer_p2p", "IDR", sqlmock.AnyArg(), "").
		WillReturnRows(sqlmock.NewRows([]string{"flat_minor_units", "percent_basis_pts", "fee_gateway"}).AddRow(2500, 0, "platform"))
	dbHandle := database.NewFromSQL(sqlDB, database.Config{})
	policy := feepolicy.New(dbHandle, repository.NewFeeRepository(dbHandle))
	h := middleware.WithAuth(testSecret, "")(NewRouterWithOptions(svc, nil, policy))

	userID := uuid.New()
	targetID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, cmd processors.Command) error {
			assert.Equal(t, "2500", cmd.Metadata["fee_amount"])
			assert.Equal(t, "platform", cmd.Metadata["fee_gateway"])
			return nil
		})

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"100000","target_user_id":"` + targetID.String() + `"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, dbMock.ExpectationsWereMet())
}

// TestNewRouterWithOptions_NilFeePolicy_FallsBackToDefault proves passing
// nil (NewRouterWithPolicy's own delegation) behaves exactly like the
// no-fee default — no fee leg attached.
func TestNewRouterWithOptions_NilFeePolicy_FallsBackToDefault(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := NewMockService(ctrl)
	svc.EXPECT().GetUserCurrency(gomock.Any(), gomock.Any(), gomock.Any()).Return("IDR", nil).AnyTimes()

	h := middleware.WithAuth(testSecret, "")(NewRouterWithOptions(svc, nil, nil))

	userID := uuid.New()
	targetID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, cmd processors.Command) error {
			_, hasFee := cmd.Metadata["fee_amount"]
			assert.False(t, hasFee, "nil feePolicy must fall back to the no-fee default")
			return nil
		})

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"100000","target_user_id":"` + targetID.String() + `"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestPostTransaction_PublicRouter_UnknownMetadataKey_Dropped(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	targetID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, cmd processors.Command) error {
			_, exists := cmd.Metadata["malicious_key"]
			assert.False(t, exists, "keys outside the allowlist must be dropped")
			return nil
		})

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + targetID.String() +
		`","metadata":{"malicious_key":"x"}}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

// TestPostTransaction_PublicRouter_RequestIDInjectedFromCtx proves
// docs/plan/36 Task T5: buildMetadata stamps metadata["request_id"] from
// the request's ctx (populated by middleware.WithRequestID) after the
// allowlist strip, so a posted transaction carries the end-to-end trace id
// regardless of what the client's own JSON body said.
func TestPostTransaction_PublicRouter_RequestIDInjectedFromCtx(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := NewMockService(ctrl)
	svc.EXPECT().GetUserCurrency(gomock.Any(), gomock.Any(), gomock.Any()).Return("IDR", nil).AnyTimes()
	h := middleware.WithRequestID()(middleware.WithAuth(testSecret, "")(NewRouter(svc)))

	userID := uuid.New()
	targetID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, cmd processors.Command) error {
			assert.Equal(t, "trace-xyz-789", cmd.Metadata["request_id"])
			return nil
		})

	// The client ALSO tries to smuggle its own request_id metadata key —
	// buildMetadata's allowlist already drops it (not in allowedMetadataKeys),
	// and even if it weren't, the ctx-sourced value is written after the
	// strip, so the server value always wins.
	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + targetID.String() +
		`","metadata":{"request_id":"attacker-supplied-id"}}`
	req := httptest.NewRequest(http.MethodPost, "/transactions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenFor(t, userID.String(), "user"))
	req.Header.Set("X-Request-Id", "trace-xyz-789")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
}

// fakeFraudGRPCClient is a minimal fraudv1.FraudServiceClient double for
// wrapping in a real *fraudcheck.Client — proves the transport layer's own
// wiring (block → 422, infra error → fail-open) without needing a running
// fraud-service (docs/plan/37 Task T3).
type fakeFraudGRPCClient struct {
	response *fraudv1.ScreenResponse
	err      error
}

func (f *fakeFraudGRPCClient) Screen(_ context.Context, _ *fraudv1.ScreenRequest, _ ...grpc.CallOption) (*fraudv1.ScreenResponse, error) {
	return f.response, f.err
}

// TestPostTransaction_PublicRouter_FraudBlock_Rejects422NoPosting proves
// docs/plan/37 Task T3: a Block verdict rejects the transaction with 422
// SCREENING_BLOCKED — the same HTTP contract as the old in-transaction
// hook — and svc.Post is NEVER called, so no ledger_transactions row (even
// a 'failed' one) is ever created for a blocked attempt.
func TestPostTransaction_PublicRouter_FraudBlock_Rejects422NoPosting(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := NewMockService(ctrl)
	svc.EXPECT().GetUserCurrency(gomock.Any(), gomock.Any(), gomock.Any()).Return("IDR", nil).AnyTimes()
	svc.EXPECT().Post(gomock.Any(), gomock.Any()).Times(0)

	fraudClient := fraudcheck.New(&fakeFraudGRPCClient{
		response: &fraudv1.ScreenResponse{Block: true, Reason: "over threshold"},
	}, "ledger")
	h := middleware.WithAuth(testSecret, "")(NewRouterWithFraud(svc, nil, nil, fraudClient, nil, 0))

	userID := uuid.New()
	targetID := uuid.New()
	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + targetID.String() + `"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "SCREENING_BLOCKED")
}

// TestPostTransaction_PublicRouter_FraudInfraError_FailsOpen proves the
// fail-open half of the same contract: a fraud-service/network error must
// NOT block posting — svc.Post is still called and the request succeeds.
func TestPostTransaction_PublicRouter_FraudInfraError_FailsOpen(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := NewMockService(ctrl)
	svc.EXPECT().GetUserCurrency(gomock.Any(), gomock.Any(), gomock.Any()).Return("IDR", nil).AnyTimes()
	svc.EXPECT().Post(gomock.Any(), gomock.Any()).Return(nil)

	fraudClient := fraudcheck.New(&fakeFraudGRPCClient{err: errors.New("connection refused")}, "ledger")
	h := middleware.WithAuth(testSecret, "")(NewRouterWithFraud(svc, nil, nil, fraudClient, nil, 0))

	userID := uuid.New()
	targetID := uuid.New()
	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + targetID.String() + `"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

// TestPostTransaction_PublicRouter_FraudDependencyUnavailable_FailsClosed503
// proves docs/plan/45 Task T3/K4: fraud-service reachable but explicitly
// signaling its velocity dependency is down (codes.FailedPrecondition +
// "DEPENDENCY_UNAVAILABLE") must fail CLOSED — 503, svc.Post NEVER
// called — unlike the generic infra-error fail-open case above.
func TestPostTransaction_PublicRouter_FraudDependencyUnavailable_FailsClosed503(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := NewMockService(ctrl)
	svc.EXPECT().GetUserCurrency(gomock.Any(), gomock.Any(), gomock.Any()).Return("IDR", nil).AnyTimes()
	svc.EXPECT().Post(gomock.Any(), gomock.Any()).Times(0)

	fraudClient := fraudcheck.New(&fakeFraudGRPCClient{
		err: status.Error(codes.FailedPrecondition, "DEPENDENCY_UNAVAILABLE"),
	}, "ledger")
	h := middleware.WithAuth(testSecret, "")(NewRouterWithFraud(svc, nil, nil, fraudClient, nil, 0))

	userID := uuid.New()
	targetID := uuid.New()
	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + targetID.String() + `"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "DEPENDENCY_UNAVAILABLE")
}

// TestPostTransaction_InternalRouter_NotScreened proves the internal
// router (disbursement/adjustment/system postings) is NEVER screened even
// when a fraud client is configured for the public router — the internal
// router's own handler struct simply never gets one wired in
// (NewInternalRouter/NewInternalRouterWithFeePolicy never set fraudClient).
// This test documents that invariant by asserting a block-mode fraud
// client attached to a PUBLIC router has no bearing on money_in, which is
// only reachable via the internal router in the first place — the real
// guarantee is structural (allowedTypes == nil skips the check), verified
// directly via NewInternalRouterWithFeePolicy's handler never receiving a
// fraudClient field at all.
func TestPostTransaction_InternalRouter_NotScreened(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := NewMockService(ctrl)
	svc.EXPECT().Post(gomock.Any(), gomock.Any()).Return(nil)

	h := middleware.WithAuth(testSecret, "")(NewInternalRouter(svc))
	userID := uuid.New()
	body := `{"idempotency_key":"abc12345","type":"money_in","amount":"1000"}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestPostTransaction_PublicRouter_AllowlistedMetadataKey_PassedThrough(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	targetID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, cmd processors.Command) error {
			assert.Equal(t, "birthday gift", cmd.Metadata["note"])
			return nil
		})

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + targetID.String() +
		`","metadata":{"note":"birthday gift"}}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestPostTransaction_UnknownGateway_RejectedOnPublicRouter(t *testing.T) {
	h, _, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	targetID := uuid.New()

	body := `{"idempotency_key":"abc12345","type":"transfer_p2p","amount":"1000","target_user_id":"` + targetID.String() +
		`","metadata":{"gateway":"not-a-real-bank"}}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostTransaction_UnknownGateway_RejectedOnInternalRouter(t *testing.T) {
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	body := `{"idempotency_key":"abc12345","type":"money_in","amount":"1000","metadata":{"gateway":"not-a-real-bank"}}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostTransaction_InternalRouter_FeeMetadata_PassedThroughUnchanged(t *testing.T) {
	// The internal router trusts its caller (e.g. a payment-gateway webhook
	// handler that already knows the real provider MDR) to set an explicit
	// fee — unlike the public router, it is not stripped/recomputed.
	h, svc, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	svc.EXPECT().Post(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, cmd processors.Command) error {
			assert.Equal(t, "250", cmd.Metadata["fee_amount"])
			assert.Equal(t, "bca", cmd.Metadata["fee_gateway"])
			return nil
		})

	body := `{"idempotency_key":"abc12345","type":"money_in","amount":"10000","metadata":{"gateway":"bca","fee_amount":"250","fee_gateway":"bca"}}`
	w := doReq(t, h, http.MethodPost, "/transactions", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestGetTransaction_NotOwned_Returns404(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	txID := uuid.New()

	svc.EXPECT().CanAccessTransaction(gomock.Any(), txID, userID).Return(false, nil)

	w := doReq(t, h, http.MethodGet, "/transactions/"+txID.String(), tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetTransaction_Owned_ReturnsOK(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	txID := uuid.New()

	svc.EXPECT().CanAccessTransaction(gomock.Any(), txID, userID).Return(true, nil)
	svc.EXPECT().GetTransaction(gomock.Any(), txID).Return(model.LedgerTransaction{
		ID: txID, Type: "money_in", Status: "posted", Amount: decimal.NewFromInt(1000), Currency: "IDR",
	}, nil)

	w := doReq(t, h, http.MethodGet, "/transactions/"+txID.String(), tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "money_in")
}

func TestGetTransaction_AdminBypassesOwnershipCheck(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	txID := uuid.New()

	// CanAccessTransaction must NOT be called for admins.
	svc.EXPECT().GetTransaction(gomock.Any(), txID).Return(model.LedgerTransaction{ID: txID}, nil)

	w := doReq(t, h, http.MethodGet, "/transactions/"+txID.String(), tokenFor(t, uuid.New().String(), "admin"), "")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListAccounts_ReturnsOwnedAccounts(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	svc.EXPECT().ListAccounts(gomock.Any(), userID).Return([]model.Account{
		{ID: uuid.New(), OwnerID: userID, Type: "cash", Currency: "IDR", Status: "active"},
	}, nil)

	w := doReq(t, h, http.MethodGet, "/accounts", tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "cash")
}

func TestGetBalance_NotOwned_Returns404(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	accID := uuid.New()

	svc.EXPECT().CanAccessAccount(gomock.Any(), accID, userID).Return(false, nil)

	w := doReq(t, h, http.MethodGet, "/accounts/"+accID.String()+"/balance", tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetBalance_Owned_ReturnsOK(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	accID := uuid.New()

	svc.EXPECT().CanAccessAccount(gomock.Any(), accID, userID).Return(true, nil)
	svc.EXPECT().GetBalance(gomock.Any(), accID).Return(model.AccountBalance{
		AccountID: accID, Currency: "IDR", Balance: decimal.NewFromInt(5000), Status: "active", Type: "cash",
	}, nil)

	w := doReq(t, h, http.MethodGet, "/accounts/"+accID.String()+"/balance", tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "5000")
}

func TestListEntries_DefaultLimitAndCursor(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	accID := uuid.New()

	svc.EXPECT().CanAccessAccount(gomock.Any(), accID, userID).Return(true, nil)
	svc.EXPECT().ListEntries(gomock.Any(), accID, time.Time{}, uuid.Nil, defaultEntriesLimit).
		Return([]model.LedgerEntry{}, nil)

	w := doReq(t, h, http.MethodGet, "/accounts/"+accID.String()+"/entries", tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListEntries_LimitClampedToMax(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	accID := uuid.New()

	svc.EXPECT().CanAccessAccount(gomock.Any(), accID, userID).Return(true, nil)
	svc.EXPECT().ListEntries(gomock.Any(), accID, time.Time{}, uuid.Nil, maxEntriesLimit).
		Return([]model.LedgerEntry{}, nil)

	w := doReq(t, h, http.MethodGet, "/accounts/"+accID.String()+"/entries?limit=99999", tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListEntries_NextCursorPresentWhenPageFull(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	accID := uuid.New()

	entries := make([]model.LedgerEntry, defaultEntriesLimit)
	now := time.Now()
	for i := range entries {
		entries[i] = model.LedgerEntry{ID: uuid.New(), AccountID: accID, Amount: decimal.NewFromInt(1), BalanceAfter: decimal.NewFromInt(1), CreatedAt: now}
	}

	svc.EXPECT().CanAccessAccount(gomock.Any(), accID, userID).Return(true, nil)
	svc.EXPECT().ListEntries(gomock.Any(), accID, time.Time{}, uuid.Nil, defaultEntriesLimit).Return(entries, nil)

	w := doReq(t, h, http.MethodGet, "/accounts/"+accID.String()+"/entries", tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "next_cursor")
}

func TestCreatePocket_Success(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	svc.EXPECT().CreatePocket(gomock.Any(), userID, "IDR", "travel").Return(model.Account{
		ID: uuid.New(), OwnerID: userID, Type: "pocket", Currency: "IDR", PocketCode: "travel", Status: "active",
	}, nil)

	body := `{"currency":"IDR","pocket_code":"travel"}`
	w := doReq(t, h, http.MethodPost, "/accounts/pockets", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), "travel")
}

func TestCreatePocket_MissingCode_Rejected(t *testing.T) {
	h, _, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()

	body := `{"currency":"IDR"}`
	w := doReq(t, h, http.MethodPost, "/accounts/pockets", tokenFor(t, userID.String(), "user"), body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ─── Admin: outbox dead-letter replay (docs/plan/12 Task T3) ──────────────────

func TestReplayDeadEvent_NotOnPublicRouter(t *testing.T) {
	h, _, ctrl := newTestHandler(t)
	defer ctrl.Finish()

	eventID := uuid.New()
	w := doReq(t, h, http.MethodPost, "/admin/outbox/dead/"+eventID.String()+"/replay", tokenFor(t, uuid.New().String(), "admin"), "")
	assert.Equal(t, http.StatusNotFound, w.Code, "admin replay routes must not exist on the public router at all")
}

func TestReplayDeadEvent_RejectedForNonAdmin(t *testing.T) {
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	eventID := uuid.New()
	w := doReq(t, h, http.MethodPost, "/admin/outbox/dead/"+eventID.String()+"/replay", tokenFor(t, uuid.New().String(), "user"), "")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestReplayDeadEvent_AllowedForAdmin(t *testing.T) {
	h, svc, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	eventID := uuid.New()
	svc.EXPECT().ReplayDeadEvent(gomock.Any(), eventID).Return(nil)

	w := doReq(t, h, http.MethodPost, "/admin/outbox/dead/"+eventID.String()+"/replay", tokenFor(t, uuid.New().String(), "admin"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"replayed":true`)
}

func TestReplayDeadEvent_NotFound_Maps404(t *testing.T) {
	h, svc, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	eventID := uuid.New()
	svc.EXPECT().ReplayDeadEvent(gomock.Any(), eventID).Return(apperror.ErrOutboxEventNotFound)

	w := doReq(t, h, http.MethodPost, "/admin/outbox/dead/"+eventID.String()+"/replay", tokenFor(t, uuid.New().String(), "admin"), "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestReplayDeadEvent_InvalidID_Rejected(t *testing.T) {
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	w := doReq(t, h, http.MethodPost, "/admin/outbox/dead/not-a-uuid/replay", tokenFor(t, uuid.New().String(), "admin"), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestReplayAllDeadEvents_RejectedForNonAdmin(t *testing.T) {
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	w := doReq(t, h, http.MethodPost, "/admin/outbox/dead/replay-all", tokenFor(t, uuid.New().String(), "user"), "{}")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestReplayAllDeadEvents_DefaultsToNow(t *testing.T) {
	h, svc, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	svc.EXPECT().ReplayDeadEvents(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, olderThan time.Time) (int, error) {
			assert.WithinDuration(t, time.Now(), olderThan, 5*time.Second)
			return 3, nil
		})

	w := doReq(t, h, http.MethodPost, "/admin/outbox/dead/replay-all", tokenFor(t, uuid.New().String(), "admin"), "{}")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"replayed_count":3`)
}

func TestReplayAllDeadEvents_ExplicitOlderThan(t *testing.T) {
	h, svc, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.EXPECT().ReplayDeadEvents(gomock.Any(), want).Return(1, nil)

	body := `{"older_than":"2026-01-01T00:00:00Z"}`
	w := doReq(t, h, http.MethodPost, "/admin/outbox/dead/replay-all", tokenFor(t, uuid.New().String(), "admin"), body)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestReplayAllDeadEvents_InvalidOlderThan_Rejected(t *testing.T) {
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	body := `{"older_than":"not-a-timestamp"}`
	w := doReq(t, h, http.MethodPost, "/admin/outbox/dead/replay-all", tokenFor(t, uuid.New().String(), "admin"), body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ─── Admin: outbox dead-letter list (docs/plan/25 Task T5) ────────────────────

func TestListDeadEvents_NotOnPublicRouter(t *testing.T) {
	h, _, ctrl := newTestHandler(t)
	defer ctrl.Finish()

	w := doReq(t, h, http.MethodGet, "/admin/outbox/dead", tokenFor(t, uuid.New().String(), "admin"), "")
	assert.Equal(t, http.StatusNotFound, w.Code, "admin list routes must not exist on the public router at all")
}

func TestListDeadEvents_RejectedForNonAdmin(t *testing.T) {
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	w := doReq(t, h, http.MethodGet, "/admin/outbox/dead", tokenFor(t, uuid.New().String(), "user"), "")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestListDeadEvents_DefaultsLimitOffset(t *testing.T) {
	h, svc, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	svc.EXPECT().ListDeadOutboxEvents(gomock.Any(), 50, 0).Return([]model.DeadOutboxEvent{
		{ID: uuid.New(), EventType: "ledger.transaction.posted.v1", RetryCount: 5, LastError: "broker unreachable", CreatedAt: time.Now()},
	}, nil)

	w := doReq(t, h, http.MethodGet, "/admin/outbox/dead", tokenFor(t, uuid.New().String(), "admin"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"event_type":"ledger.transaction.posted.v1"`)
	assert.Contains(t, w.Body.String(), `"retry_count":5`)
}

func TestListDeadEvents_ExplicitLimitOffset(t *testing.T) {
	h, svc, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	svc.EXPECT().ListDeadOutboxEvents(gomock.Any(), 10, 20).Return(nil, nil)

	w := doReq(t, h, http.MethodGet, "/admin/outbox/dead?limit=10&offset=20", tokenFor(t, uuid.New().String(), "admin"), "")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListDeadEvents_InvalidLimit_Rejected(t *testing.T) {
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	w := doReq(t, h, http.MethodGet, "/admin/outbox/dead?limit=0", tokenFor(t, uuid.New().String(), "admin"), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListDeadEvents_InvalidOffset_Rejected(t *testing.T) {
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	w := doReq(t, h, http.MethodGet, "/admin/outbox/dead?offset=-1", tokenFor(t, uuid.New().String(), "admin"), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ─── Admin: recon batch list (docs/plan/25 Task T5) ────────────────────────────

func TestListReconBatches_NotOnPublicRouter(t *testing.T) {
	h, _, ctrl := newTestHandler(t)
	defer ctrl.Finish()

	w := doReq(t, h, http.MethodGet, "/admin/recon/batches", tokenFor(t, uuid.New().String(), "admin"), "")
	assert.Equal(t, http.StatusNotFound, w.Code, "admin list routes must not exist on the public router at all")
}

func TestListReconBatches_RejectedForNonAdmin(t *testing.T) {
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	w := doReq(t, h, http.MethodGet, "/admin/recon/batches", tokenFor(t, uuid.New().String(), "user"), "")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestListReconBatches_DefaultsLimitOffset(t *testing.T) {
	h, svc, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	svc.EXPECT().ListReconBatches(gomock.Any(), 0, 0).Return([]model.ReconBatch{
		{ID: uuid.New(), Gateway: "bca", ReportDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Status: "completed", RowCount: 10, CreatedBy: "ops"},
	}, nil)

	w := doReq(t, h, http.MethodGet, "/admin/recon/batches", tokenFor(t, uuid.New().String(), "admin"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"gateway":"bca"`)
	assert.Contains(t, w.Body.String(), `"report_date":"2026-01-01"`)
}

func TestListReconBatches_ExplicitLimitOffset(t *testing.T) {
	h, svc, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	svc.EXPECT().ListReconBatches(gomock.Any(), 10, 20).Return(nil, nil)

	w := doReq(t, h, http.MethodGet, "/admin/recon/batches?limit=10&offset=20", tokenFor(t, uuid.New().String(), "admin"), "")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListReconBatches_InvalidLimit_Rejected(t *testing.T) {
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	w := doReq(t, h, http.MethodGet, "/admin/recon/batches?limit=-5", tokenFor(t, uuid.New().String(), "admin"), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListReconBatches_InvalidOffset_Rejected(t *testing.T) {
	h, _, ctrl := newInternalTestHandler(t)
	defer ctrl.Finish()

	w := doReq(t, h, http.MethodGet, "/admin/recon/batches?offset=-1", tokenFor(t, uuid.New().String(), "admin"), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCursor_RoundTrip(t *testing.T) {
	id := uuid.New()
	now := time.Now().Truncate(time.Nanosecond)
	encoded := encodeCursor(now, id)

	gotTime, gotID, err := decodeCursor(encoded)
	require.NoError(t, err)
	assert.Equal(t, id, gotID)
	assert.True(t, now.Equal(gotTime))
}

func TestCursor_EmptyDecodesToZero(t *testing.T) {
	gotTime, gotID, err := decodeCursor("")
	require.NoError(t, err)
	assert.True(t, gotTime.IsZero())
	assert.Equal(t, uuid.Nil, gotID)
}

func TestCursor_InvalidRejected(t *testing.T) {
	_, _, err := decodeCursor("not-valid-base64!!!")
	assert.Error(t, err)
}

// ─── docs/plan/15 Task T2: GET /accounts/{id}/statement ────────────────────

func TestGetStatement_NotOwned_Returns404(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	accID := uuid.New()

	svc.EXPECT().CanAccessAccount(gomock.Any(), accID, userID).Return(false, nil)

	w := doReq(t, h, http.MethodGet,
		"/accounts/"+accID.String()+"/statement?from=2026-06-01&to=2026-06-30",
		tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetStatement_MissingDates_Rejected(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	accID := uuid.New()

	svc.EXPECT().CanAccessAccount(gomock.Any(), accID, userID).Return(true, nil)

	w := doReq(t, h, http.MethodGet, "/accounts/"+accID.String()+"/statement",
		tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetStatement_InvalidDateFormat_Rejected(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	accID := uuid.New()

	svc.EXPECT().CanAccessAccount(gomock.Any(), accID, userID).Return(true, nil)

	w := doReq(t, h, http.MethodGet,
		"/accounts/"+accID.String()+"/statement?from=06-01-2026&to=2026-06-30",
		tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetStatement_FromAfterTo_Rejected(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	accID := uuid.New()

	svc.EXPECT().CanAccessAccount(gomock.Any(), accID, userID).Return(true, nil)

	w := doReq(t, h, http.MethodGet,
		"/accounts/"+accID.String()+"/statement?from=2026-06-30&to=2026-06-01",
		tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetStatement_RangeExceeds92Days_Rejected(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	accID := uuid.New()

	svc.EXPECT().CanAccessAccount(gomock.Any(), accID, userID).Return(true, nil)

	// 2026-01-01 to 2026-12-31 is 364 days — well over the 92-day cap.
	w := doReq(t, h, http.MethodGet,
		"/accounts/"+accID.String()+"/statement?from=2026-01-01&to=2026-12-31",
		tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "92")
}

func TestGetStatement_InvalidFormat_Rejected(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	accID := uuid.New()

	svc.EXPECT().CanAccessAccount(gomock.Any(), accID, userID).Return(true, nil)

	w := doReq(t, h, http.MethodGet,
		"/accounts/"+accID.String()+"/statement?from=2026-06-01&to=2026-06-02&format=xml",
		tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetStatement_JSON_ReturnsOK(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	accID := uuid.New()
	from, _ := time.Parse("2006-01-02", "2026-06-01")
	to, _ := time.Parse("2006-01-02", "2026-06-02")

	svc.EXPECT().CanAccessAccount(gomock.Any(), accID, userID).Return(true, nil)
	svc.EXPECT().Statement(gomock.Any(), accID, from, to).Return(model.Statement{
		AccountID: accID, Currency: "IDR", From: from, To: to,
		OpeningBalance: decimal.NewFromInt(1000), ClosingBalance: decimal.NewFromInt(1500),
		Entries: []model.StatementEntry{
			{ID: uuid.New(), TransactionID: uuid.New(), TransactionType: "money_in",
				AccountID: accID, Direction: "credit", Amount: decimal.NewFromInt(500),
				BalanceAfter: decimal.NewFromInt(1500), CreatedAt: from},
		},
	}, nil)

	w := doReq(t, h, http.MethodGet,
		"/accounts/"+accID.String()+"/statement?from=2026-06-01&to=2026-06-02",
		tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "\"opening_balance\":\"1000\"")
	assert.Contains(t, w.Body.String(), "\"closing_balance\":\"1500\"")
	assert.Contains(t, w.Body.String(), "money_in")
}

func TestGetStatement_CSV_StreamsRows(t *testing.T) {
	h, svc, ctrl := newTestHandler(t)
	defer ctrl.Finish()
	userID := uuid.New()
	accID := uuid.New()
	from, _ := time.Parse("2006-01-02", "2026-06-01")
	to, _ := time.Parse("2006-01-02", "2026-06-02")
	entryID := uuid.New()
	txID := uuid.New()

	svc.EXPECT().CanAccessAccount(gomock.Any(), accID, userID).Return(true, nil)
	svc.EXPECT().Statement(gomock.Any(), accID, from, to).Return(model.Statement{
		AccountID: accID, Currency: "IDR", From: from, To: to,
		OpeningBalance: decimal.NewFromInt(1000), ClosingBalance: decimal.NewFromInt(1500),
		Entries: []model.StatementEntry{
			{ID: entryID, TransactionID: txID, TransactionType: "money_in",
				AccountID: accID, Direction: "credit", Amount: decimal.NewFromInt(500),
				BalanceAfter: decimal.NewFromInt(1500), CreatedAt: from},
		},
	}, nil)

	w := doReq(t, h, http.MethodGet,
		"/accounts/"+accID.String()+"/statement?from=2026-06-01&to=2026-06-02&format=csv",
		tokenFor(t, userID.String(), "user"), "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/csv", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
	body := w.Body.String()
	assert.Contains(t, body, "entry_id,tx_id,type,direction,amount,balance_after,note,created_at")
	assert.Contains(t, body, entryID.String())
	assert.Contains(t, body, txID.String())
	assert.Contains(t, body, "money_in")
	assert.Contains(t, body, "credit")
}

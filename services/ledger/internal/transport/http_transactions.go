package transport

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	fraudcheck "github.com/herdifirdausss/seev/contracts/clients/fraud"
	"github.com/herdifirdausss/seev/internal/platform/money/currency"
	"github.com/herdifirdausss/seev/internal/platform/security/middleware"
	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/ledger/internal/feepolicy"
	commandservice "github.com/herdifirdausss/seev/services/ledger/internal/ledger/command"
	apperror "github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/governance"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/processors"
)

// scheduleCurrencyCreator is optional so existing transport test doubles and
// older embedders can keep implementing Service without a breaking interface
// change. The production ledger.Module implements it for explicit currencies.
type scheduleCurrencyCreator interface {
	CreateScheduleWithCurrency(ctx context.Context, userID uuid.UUID, txType string, amount decimal.Decimal, targetUserID uuid.UUID, pocketCode string, metadata map[string]any, kind string, runAtDate time.Time, dayOfMonth *int, createdBy, currency string) (uuid.UUID, error)
}

type handler struct {
	svc Service
	// allowedTypes restricts which transaction types postTransaction accepts.
	// nil means every registered type is allowed (internal router);
	// non-nil is an explicit allowlist (public router).
	allowedTypes map[string]bool
	// feePolicy computes server-side fees for the public router
	// (docs/roadmap/archive/10 Task T3) — see buildMetadata in metadata.go.
	feePolicy *feepolicy.Policy
	// policy evaluates per-user/per-type limits (docs/roadmap/archive/17 Task T1)
	// before postTransaction calls svc.Post. nil on the internal router —
	// trusted internal callers (payment-gateway webhooks, ops tooling) are
	// not subject to end-user velocity limits.
	policy PolicyChecker
	// executor is the production money-movement boundary. Legacy constructors
	// leave it nil for isolated transport tests and compatibility embedders.
	executor    *commandservice.Executor
	feeApproval *feepolicy.Approval
	// fraudClient screens a candidate transaction BEFORE any DB work
	// (docs/roadmap/archive/37) — nil on the internal router (disbursement/adjustment/
	// system postings are not user-flow screening targets) and nil-safe on
	// the public router too (screening simply skipped if never configured,
	// same "absent = no-op" convention as feePolicy/policy above).
	fraudClient *fraudcheck.Client
	logger      *slog.Logger
	// feeQuoteTTL is how long a quote created via POST /fees/quote
	// (docs/roadmap/archive/38 Task T3) stays consumable. <=0 falls back to
	// feepolicy.DefaultQuoteTTL.
	feeQuoteTTL time.Duration
}

// PolicyChecker is satisfied structurally by services/ledger/policy.Engine —
// defined here, not imported from there, so the ledger module never
// depends on services/ledger/policy (docs/roadmap/archive/17 Task T1, decision K-S S1:
// "the ledger module must not know about policy limits").
type PolicyChecker interface {
	// Check reports whether userID may post a txType transaction of the
	// given amount. allowed=false means reject; rule names which limit
	// dimension was violated, detail is a human-readable message.
	Check(ctx context.Context, userID uuid.UUID, txType string, amount decimal.Decimal) (allowed bool, rule string, detail string, err error)
	// Record registers a transaction that already posted successfully —
	// callers must NEVER call this for a transaction that failed to post.
	Record(ctx context.Context, userID uuid.UUID, txType string, amount decimal.Decimal)
}

type currencyPolicyChecker interface {
	CheckWithCurrency(ctx context.Context, userID uuid.UUID, txType string, amount decimal.Decimal, currency string) (allowed bool, rule string, detail string, err error)
	RecordWithCurrency(ctx context.Context, userID uuid.UUID, txType string, amount decimal.Decimal, currency string)
}

// currentUserID extracts and parses the authenticated user's ID from the JWT
// claims already validated by internal/platform/security/middleware.WithAuth.
func currentUserID(r *http.Request) (uuid.UUID, bool) {
	raw := middleware.UserIDFromCtx(r.Context())
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func isAdmin(r *http.Request) bool {
	claims := middleware.GetClaims(r.Context())
	return claims != nil && (claims.Role == "admin" || claims.Role == "admin_maker" || claims.Role == "admin_checker")
}

func isAdminMaker(r *http.Request) bool {
	claims := middleware.GetClaims(r.Context())
	return claims != nil && (claims.Role == "admin" || claims.Role == "admin_maker")
}

func isAdminChecker(r *http.Request) bool {
	claims := middleware.GetClaims(r.Context())
	return claims != nil && (claims.Role == "admin" || claims.Role == "admin_checker")
}

func (h *handler) postTransaction(w http.ResponseWriter, r *http.Request) {
	if h.allowedTypes != nil {
		claims := middleware.GetClaims(r.Context())
		if claims == nil || claims.KYCLevel < 1 {
			response.JSON(w, http.StatusForbidden, map[string]any{"code": "KYC_REQUIRED", "min_level": 1})
			return
		}
	}
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}

	var req postTransactionRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.Currency != "" {
		if err := currency.ValidateCode(req.Currency); err != nil {
			response.BadRequest(w, "currency must be three uppercase ASCII letters")
			return
		}
	}

	if len(req.IdempotencyKey) < 8 || len(req.IdempotencyKey) > 128 {
		response.BadRequest(w, "idempotency_key must be between 8 and 128 characters")
		return
	}
	if req.Type == "" {
		response.BadRequest(w, "type is required")
		return
	}
	if h.allowedTypes != nil && !h.allowedTypes[req.Type] {
		response.Forbidden(w, "this transaction type is not available on the public API")
		return
	}
	if governance.IsWorkflowOnlyPostType(req.Type) {
		message := "this transaction type cannot be posted directly"
		switch req.Type {
		case "fx_out", "fx_in":
			message = "FX primitives can only be posted by an atomic FX conversion"
		case "fx_rebalance_credit", "fx_rebalance_debit":
			message = "FX rebalances must use POST /admin/fx/rebalances"
		default:
			message += " — use POST /admin/adjustments (requires a second identity to approve)"
		}
		response.Forbidden(w, message)
		return
	}
	if adminOnlyTypes[req.Type] && !isAdmin(r) {
		response.Forbidden(w, "this transaction type requires admin privileges")
		return
	}

	amount, err := decimalFromString(req.Amount)
	if err != nil {
		if errors.Is(err, errNonIntegralAmount) {
			response.BadRequest(w, err.Error())
		} else {
			response.BadRequest(w, "amount must be a valid decimal string")
		}
		return
	}

	// Idempotency scope defaults to the caller's own userID — this is the
	// ONLY behavior on the public router (allowedTypes != nil): a client
	// cannot spoof another user's scope or collide with their keys. The
	// internal router (allowedTypes == nil, trusted caller) may pass an
	// explicit scope — e.g. a payment-gateway webhook handler scoping by
	// provider transaction id rather than by end-user (docs/roadmap/archive/10 T2).
	idemScope := userID.String()
	if h.allowedTypes == nil && req.IdempotencyScope != "" {
		idemScope = req.IdempotencyScope
	}

	// Metadata is never passed through verbatim on the public router — see
	// buildMetadata (metadata.go, docs/roadmap/archive/10 Task T3): gateway is
	// validated, fee_amount/fee_gateway are stripped and replaced with a
	// server-computed fee, and only a small set of descriptive keys survive.
	metadata, err := h.buildMetadata(r.Context(), userID, req, amount)
	if err != nil {
		response.BadRequest(w, err.Error())
		return
	}

	cmd := processors.Command{
		IdempotencyKey:   req.IdempotencyKey,
		IdempotencyScope: idemScope,
		Type:             req.Type,
		Amount:           amount,
		UserID:           userID,
		Currency:         req.Currency,
		PocketCode:       req.PocketCode,
		Metadata:         metadata,
		QuoteID:          req.QuoteID,
	}
	if req.TargetUserID != "" {
		targetID, err := uuid.Parse(req.TargetUserID)
		if err != nil {
			response.BadRequest(w, "target_user_id must be a valid UUID")
			return
		}
		cmd.TargetUserID = targetID
	}
	if req.ReferenceID != "" {
		refID, err := uuid.Parse(req.ReferenceID)
		if err != nil {
			response.BadRequest(w, "reference_id must be a valid UUID")
			return
		}
		cmd.ReferenceID = refID
	}

	// Resolve only the currency needed to construct the execution context. The
	// policy decision itself belongs to command.Executor, never to transport.
	policyCurrency := req.Currency
	if (h.policy != nil || h.executor != nil) && policyCurrency == "" {
		if resolved, resolveErr := h.svc.GetUserCurrency(r.Context(), userID, req.PocketCode); resolveErr == nil {
			policyCurrency = resolved
		}
	}

	// AML/fraud screening (docs/roadmap/archive/37) — runs on the PUBLIC router ONLY,
	// same layer as the policy check above, BEFORE svc.Post ever opens a DB
	// transaction. This replaces the old in-transaction PrePostHook seam
	// (docs/roadmap/archive/20), which held a FOR UPDATE row lock for up to 500ms per
	// posting waiting on this exact same network round-trip. h.allowedTypes
	// is nil on the internal router (disbursement/adjustment/system
	// postings) — screening is a user-flow control, not applied there.
	if h.fraudClient != nil && h.allowedTypes != nil {
		screenCurrency := req.Currency
		if screenCurrency == "" {
			var cerr error
			screenCurrency, cerr = h.svc.GetUserCurrency(r.Context(), userID, req.PocketCode)
			if cerr != nil {
				screenCurrency = ""
			}
		}
		verdict, ferr := h.fraudClient.Check(r.Context(), "p2p_transfer", req.Type, userID, amount, screenCurrency)
		if ferr != nil {
			if errors.Is(ferr, fraudcheck.ErrDependencyUnavailable) {
				// docs/roadmap/archive/45 Task T3/K4: fraud-service is reachable but its
				// velocity dependency is down — fail CLOSED, unlike every
				// other Check error below (fail open). No posting has
				// happened yet.
				h.logger.Warn("screening dependency unavailable, failing closed", "type", req.Type)
				response.ServiceUnavailable(w, "DEPENDENCY_UNAVAILABLE", "fraud screening dependency unavailable")
				return
			}
			h.logger.Error("screening check error, failing open", "error", ferr, "type", req.Type)
		} else if verdict.Block {
			writeError(w, apperror.NewBizErr(apperror.ErrScreeningBlocked, verdict.Reason))
			return
		}
	}

	if h.executor != nil {
		source := "internal-service"
		if h.allowedTypes != nil {
			source = "public-api"
		}
		claims := middleware.GetClaims(r.Context())
		claimValues := map[string]string{}
		if claims != nil {
			claimValues["role"] = claims.Role
			claimValues["kyc_level"] = strconv.Itoa(claims.KYCLevel)
		}
		if err := h.executor.Execute(r.Context(), cmd, commandservice.ExecutionContext{
			ActorID: userID, Source: source, CorrelationID: middleware.RequestIDFromCtx(r.Context()),
			AuthorizationClaims: claimValues, RequestOrigin: r.Method + " " + r.URL.Path,
			Currency: policyCurrency,
		}); err != nil {
			writeError(w, err)
			return
		}
	} else {
		// Compatibility constructors used by narrow transport tests retain the
		// historical policy seam. Production wiring always supplies executor.
		if h.policy != nil {
			var allowed bool
			var rule, detail string
			var policyErr error
			if checker, ok := h.policy.(currencyPolicyChecker); ok && policyCurrency != "" {
				allowed, rule, detail, policyErr = checker.CheckWithCurrency(r.Context(), userID, req.Type, amount, policyCurrency)
			} else {
				allowed, rule, detail, policyErr = h.policy.Check(r.Context(), userID, req.Type, amount)
			}
			if policyErr != nil {
				response.InternalServerError(w, policyErr)
				return
			}
			if !allowed {
				response.UnprocessableEntity(w, fmt.Sprintf("policy limit exceeded (%s): %s", rule, detail))
				return
			}
		}
		if err := h.svc.Post(r.Context(), cmd); err != nil {
			writeError(w, err)
			return
		}
		if h.policy != nil {
			if recorder, ok := h.policy.(currencyPolicyChecker); ok && policyCurrency != "" {
				recorder.RecordWithCurrency(r.Context(), userID, req.Type, amount, policyCurrency)
			} else {
				h.policy.Record(r.Context(), userID, req.Type, amount)
			}
		}
	}

	response.Created(w, postTransactionResponse{Status: "posted", IdempotencyKey: req.IdempotencyKey})
}

func (h *handler) getTransaction(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	txID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid transaction id")
		return
	}

	if !isAdmin(r) {
		allowed, err := h.svc.CanAccessTransaction(r.Context(), txID, userID)
		if err != nil {
			writeError(w, err)
			return
		}
		if !allowed {
			response.NotFound(w, "transaction not found")
			return
		}
	}

	tx, err := h.svc.GetTransaction(r.Context(), txID)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, toTransactionResponse(tx))
}

func (h *handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	accounts, err := h.svc.ListAccounts(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]accountResponse, len(accounts))
	for i, a := range accounts {
		out[i] = toAccountResponse(a)
	}
	response.OK(w, out)
}

func (h *handler) getBalance(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	accountID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid account id")
		return
	}

	if !isAdmin(r) {
		allowed, err := h.svc.CanAccessAccount(r.Context(), accountID, userID)
		if err != nil {
			writeError(w, err)
			return
		}
		if !allowed {
			response.NotFound(w, "account not found")
			return
		}
	}

	asOfRaw := r.URL.Query().Get("as_of")
	if asOfRaw == "" {
		bal, err := h.svc.GetBalance(r.Context(), accountID)
		if err != nil {
			writeError(w, err)
			return
		}
		response.OK(w, toBalanceResponse(bal))
		return
	}

	asOf, err := time.Parse("2006-01-02", asOfRaw)
	if err != nil {
		response.BadRequest(w, "as_of must be YYYY-MM-DD")
		return
	}
	bal, err := h.svc.GetBalanceAsOf(r.Context(), accountID, asOf)
	if err != nil {
		writeError(w, err)
		return
	}
	out := toBalanceResponse(bal)
	out.AsOf = asOfRaw
	response.OK(w, out)
}

func (h *handler) listEntries(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	accountID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid account id")
		return
	}

	if !isAdmin(r) {
		allowed, err := h.svc.CanAccessAccount(r.Context(), accountID, userID)
		if err != nil {
			writeError(w, err)
			return
		}
		if !allowed {
			response.NotFound(w, "account not found")
			return
		}
	}

	limit := defaultEntriesLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			response.BadRequest(w, "limit must be a positive integer")
			return
		}
		limit = min(parsed, maxEntriesLimit)
	}

	beforeCreatedAt, beforeID, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		response.BadRequest(w, err.Error())
		return
	}

	entries, err := h.svc.ListEntries(r.Context(), accountID, beforeCreatedAt, beforeID, limit)
	if err != nil {
		writeError(w, err)
		return
	}

	out := listEntriesResponse{Entries: make([]entryResponse, len(entries))}
	for i, e := range entries {
		out.Entries[i] = toEntryResponse(e)
	}
	if len(entries) == limit {
		last := entries[len(entries)-1]
		out.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	response.OK(w, out)
}

// getStatement handles GET /accounts/{id}/statement?from=&to=&format=
// (docs/roadmap/archive/15 Task T2). from/to are inclusive Asia/Jakarta calendar dates.
// format defaults to json; csv streams row-by-row rather than buffering the
// whole (up to 5,000-row) body in memory.
func (h *handler) getStatement(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}
	accountID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid account id")
		return
	}

	if !isAdmin(r) {
		allowed, err := h.svc.CanAccessAccount(r.Context(), accountID, userID)
		if err != nil {
			writeError(w, err)
			return
		}
		if !allowed {
			response.NotFound(w, "account not found")
			return
		}
	}
	if !isAdmin(r) {
		if validator, ok := h.svc.(interface {
			ValidateCurrency(context.Context, string, string) error
		}); ok {
			balance, berr := h.svc.GetBalance(r.Context(), accountID)
			if berr != nil {
				writeError(w, berr)
				return
			}
			if verr := validator.ValidateCurrency(r.Context(), balance.Currency, "statement_read"); verr != nil {
				writeError(w, verr)
				return
			}
		}
	}

	fromRaw := r.URL.Query().Get("from")
	toRaw := r.URL.Query().Get("to")
	if fromRaw == "" || toRaw == "" {
		response.BadRequest(w, "from and to are required (YYYY-MM-DD)")
		return
	}
	from, err := time.Parse("2006-01-02", fromRaw)
	if err != nil {
		response.BadRequest(w, "from must be YYYY-MM-DD")
		return
	}
	to, err := time.Parse("2006-01-02", toRaw)
	if err != nil {
		response.BadRequest(w, "to must be YYYY-MM-DD")
		return
	}
	if from.After(to) {
		response.BadRequest(w, "from must not be after to")
		return
	}
	if to.Sub(from) > maxStatementDays*24*time.Hour {
		response.BadRequest(w, fmt.Sprintf("range too large, narrow the period (max %d days)", maxStatementDays))
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		response.BadRequest(w, "format must be 'json' or 'csv'")
		return
	}

	stmt, err := h.svc.Statement(r.Context(), accountID, from, to)
	if err != nil {
		writeError(w, err)
		return
	}

	if format == "csv" {
		writeStatementCSV(w, stmt)
		return
	}
	response.OK(w, toStatementResponse(stmt))
}

// writeStatementCSV streams the statement row by row — never buffers the
// full (up to 5,000-row) body in memory (docs/roadmap/archive/15 Task T2, decision K7,
// box-size constraint). encoding/csv handles RFC 4180 escaping.
func writeStatementCSV(w http.ResponseWriter, stmt model.Statement) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(
		"attachment; filename=statement_%s_%s_%s.csv",
		stmt.AccountID, stmt.From.Format("2006-01-02"), stmt.To.Format("2006-01-02")))
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	minorUnit, _ := currency.MinorUnit(stmt.Currency)
	_ = cw.Write([]string{"currency", "minor_unit", "entry_id", "tx_id", "type", "direction", "amount", "balance_after", "note", "created_at"})
	for _, e := range stmt.Entries {
		_ = cw.Write([]string{
			stmt.Currency, strconv.Itoa(int(minorUnit)), e.ID.String(), e.TransactionID.String(), e.TransactionType, string(e.Direction),
			e.Amount.String(), e.BalanceAfter.String(), e.Note, e.CreatedAt.Format(time.RFC3339),
		})
	}
	cw.Flush()
}

func (h *handler) createPocket(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		response.Unauthorized(w, "invalid or missing user identity")
		return
	}

	var req createPocketRequest
	if !response.Decode(w, r, &req) {
		return
	}
	if req.Currency == "" {
		response.BadRequest(w, "currency is required")
		return
	}
	if req.PocketCode == "" {
		response.BadRequest(w, "pocket_code is required")
		return
	}

	acc, err := h.svc.CreatePocket(r.Context(), userID, req.Currency, req.PocketCode)
	if err != nil {
		writeError(w, err)
		return
	}
	response.Created(w, toAccountResponse(acc))
}

package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/response"
)

type createPayoutRequest struct {
	Amount      string          `json:"amount"`
	Destination json.RawMessage `json:"destination"`
	// QuoteID (docs/plan/38 Task T5), when set, consumes a fee quote
	// BEFORE the hold is posted — the fee it locks in is honored at settle
	// time regardless of any fee_rules change in between.
	QuoteID string `json:"quote_id,omitempty"`
}
type payoutResponse struct {
	ID           uuid.UUID `json:"id"`
	UserID       uuid.UUID `json:"user_id"`
	Amount       string    `json:"amount"`
	Currency     string    `json:"currency"`
	Vendor       string    `json:"vendor"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func payoutFromProto(value *payoutv1.Payout) payoutResponse {
	id, _ := uuid.Parse(value.GetId())
	user, _ := uuid.Parse(value.GetUserId())
	return payoutResponse{ID: id, UserID: user, Amount: value.GetAmount(), Currency: value.GetCurrency(), Vendor: value.GetVendor(), Status: value.GetStatus(), ErrorMessage: value.GetErrorMessage(), CreatedAt: value.GetCreatedAt().AsTime(), UpdatedAt: value.GetUpdatedAt().AsTime()}
}

func createPayoutHandler(client payoutv1.PayoutServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := uuid.Parse(middleware.UserIDFromCtx(r.Context()))
		if err != nil {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		var request createPayoutRequest
		if !response.Decode(w, r, &request) {
			return
		}
		if len(request.Destination) == 0 {
			response.BadRequest(w, "destination is required")
			return
		}
		amount, err := decimal.NewFromString(request.Amount)
		if err != nil || !amount.Equal(amount.Truncate(0)) {
			response.BadRequest(w, "amount must be a valid integer decimal string")
			return
		}
		if !amount.IsPositive() {
			response.BadRequest(w, "amount must be positive")
			return
		}
		result, err := client.CreatePayout(r.Context(), &payoutv1.CreatePayoutRequest{UserId: userID.String(), Amount: amount.String(), Destination: request.Destination, CreatedBy: userID.String(), QuoteId: request.QuoteID})
		if err != nil {
			if status.Code(err) == codes.FailedPrecondition {
				msg := status.Convert(err).Message()
				switch {
				case msg == "no payout route available":
					response.JSON(w, http.StatusUnprocessableEntity, response.Envelope{Success: false, Error: &response.Error{Code: "NO_ROUTE", Message: "no payout route available"}})
				case strings.HasPrefix(msg, "payout: screening blocked"):
					// docs/plan/37 Task T5 — same HTTP contract as the
					// ledger's own SCREENING_BLOCKED (docs/plan/37 Task T3):
					// no payout_requests row was ever created.
					response.JSON(w, http.StatusUnprocessableEntity, response.Envelope{Success: false, Error: &response.Error{Code: "SCREENING_BLOCKED", Message: msg}})
				case strings.HasPrefix(msg, "[QUOTE_EXPIRED]"):
					// docs/plan/38 Task T5 — ledgerclient.ConsumeFeeQuote's
					// error decodes via pkg/ledgererr.FromStatus's GENERIC
					// FailedPrecondition handling into
					// "[QUOTE_EXPIRED] <message>" (ledgererr.LedgerError.Error's
					// own format) — no dedicated ledgererr sentinel needed,
					// same free-parity mechanism as SCREENING_BLOCKED.
					response.JSON(w, http.StatusUnprocessableEntity, response.Envelope{Success: false, Error: &response.Error{Code: "QUOTE_EXPIRED", Message: msg}})
				case strings.HasPrefix(msg, "[QUOTE_MISMATCH]"):
					response.JSON(w, http.StatusUnprocessableEntity, response.Envelope{Success: false, Error: &response.Error{Code: "QUOTE_MISMATCH", Message: msg}})
				default:
					response.UnprocessableEntity(w, msg)
				}
			} else {
				response.InternalServerError(w, err)
			}
			return
		}
		response.Created(w, payoutFromProto(result.GetPayout()))
	}
}
func getPayoutHandler(client payoutv1.PayoutServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := uuid.Parse(middleware.UserIDFromCtx(r.Context()))
		if err != nil {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			response.BadRequest(w, "invalid payout id")
			return
		}
		result, err := client.GetPayout(r.Context(), &payoutv1.GetPayoutRequest{Id: id.String(), UserId: userID.String()})
		if err != nil {
			if status.Code(err) == codes.NotFound {
				response.NotFound(w, "payout request not found")
			} else {
				response.InternalServerError(w, err)
			}
			return
		}
		response.OK(w, payoutFromProto(result.GetPayout()))
	}
}

package handler

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/response"
)

type createTopupRequest struct {
	Amount string `json:"amount"`
}
type topupResponse struct {
	ID        uuid.UUID `json:"id"`
	Reference string    `json:"reference"`
	UserID    uuid.UUID `json:"user_id"`
	Amount    string    `json:"amount"`
	Currency  string    `json:"currency"`
	Vendor    string    `json:"vendor"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func topupFromProto(value *payinv1.TopupIntent) topupResponse {
	id, _ := uuid.Parse(value.GetId())
	userID, _ := uuid.Parse(value.GetUserId())
	return topupResponse{ID: id, Reference: value.GetReference(), UserID: userID, Amount: value.GetAmount(), Currency: value.GetCurrency(), Vendor: value.GetVendor(), Status: value.GetStatus(), ExpiresAt: value.GetExpiresAt().AsTime(), CreatedAt: value.GetCreatedAt().AsTime(), UpdatedAt: value.GetUpdatedAt().AsTime()}
}

func createTopupIntentHandler(client payinv1.PayinServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := uuid.Parse(middleware.UserIDFromCtx(r.Context()))
		if err != nil {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		var request createTopupRequest
		if !response.Decode(w, r, &request) {
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
		result, err := client.CreateTopupIntent(r.Context(), &payinv1.CreateTopupIntentRequest{UserId: userID.String(), Amount: amount.String()})
		if err != nil {
			if status.Code(err) == codes.FailedPrecondition {
				response.JSON(w, http.StatusUnprocessableEntity, response.Envelope{Success: false, Error: &response.Error{Code: "NO_ROUTE", Message: "no topup route available"}})
			} else {
				response.InternalServerError(w, err)
			}
			return
		}
		response.Created(w, topupFromProto(result.GetIntent()))
	}
}

func getTopupIntentHandler(client payinv1.PayinServiceClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := uuid.Parse(middleware.UserIDFromCtx(r.Context()))
		if err != nil {
			response.Unauthorized(w, "invalid or missing user identity")
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			response.BadRequest(w, "invalid topup id")
			return
		}
		result, err := client.GetTopupIntent(r.Context(), &payinv1.GetTopupIntentRequest{Id: id.String(), UserId: userID.String()})
		if err != nil {
			if status.Code(err) == codes.NotFound {
				response.NotFound(w, "topup intent not found")
			} else {
				response.InternalServerError(w, err)
			}
			return
		}
		response.OK(w, topupFromProto(result.GetIntent()))
	}
}

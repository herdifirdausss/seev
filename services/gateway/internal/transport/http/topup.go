package http

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	payinv1 "github.com/herdifirdausss/seev/gen/go/payin/v1"
	currencyreg "github.com/herdifirdausss/seev/internal/platform/money/currency"
	"github.com/herdifirdausss/seev/internal/platform/security/middleware"
	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
)

type createTopupRequest struct {
	Amount     string `json:"amount"`
	Currency   string `json:"currency,omitempty"`
	FeeQuoteID string `json:"fee_quote_id,omitempty"`
}
type topupResponse struct {
	ID             uuid.UUID `json:"id"`
	Reference      string    `json:"reference"`
	UserID         uuid.UUID `json:"user_id"`
	Amount         string    `json:"amount"`
	FeeAmount      string    `json:"fee_amount"`
	TotalDebit     string    `json:"total_debit"`
	FeeGateway     string    `json:"fee_gateway,omitempty"`
	FeeQuoteID     string    `json:"fee_quote_id,omitempty"`
	FeeApplication string    `json:"fee_application"`
	Currency       string    `json:"currency"`
	Vendor         string    `json:"vendor"`
	Status         string    `json:"status"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func topupFromProto(value *payinv1.TopupIntent) topupResponse {
	id, _ := uuid.Parse(value.GetId())
	userID, _ := uuid.Parse(value.GetUserId())
	return topupResponse{ID: id, Reference: value.GetReference(), UserID: userID, Amount: value.GetAmount(),
		FeeAmount: protoStringField(value, "fee_amount"), TotalDebit: protoStringField(value, "total_debit"),
		FeeGateway: protoStringField(value, "fee_gateway"), FeeQuoteID: protoStringField(value, "fee_quote_id"),
		FeeApplication: protoStringField(value, "fee_application"), Currency: value.GetCurrency(), Vendor: value.GetVendor(), Status: value.GetStatus(), ExpiresAt: value.GetExpiresAt().AsTime(), CreatedAt: value.GetCreatedAt().AsTime(), UpdatedAt: value.GetUpdatedAt().AsTime()}
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
		if request.Currency != "" {
			if err := currencyreg.ValidateCode(request.Currency); err != nil {
				response.ErrorStatus(w, http.StatusBadRequest, "CURRENCY_INVALID", "currency must be exactly three uppercase letters")
				return
			}
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
		protoRequest := &payinv1.CreateTopupIntentRequest{UserId: userID.String(), Amount: amount.String()}
		setProtoStringField(protoRequest, "fee_quote_id", request.FeeQuoteID)
		callContext := r.Context()
		if request.Currency != "" {
			callContext = metadata.AppendToOutgoingContext(callContext, "x-seev-currency", request.Currency)
		}
		if request.FeeQuoteID != "" {
			// The checked-in generated client may predate the additive proto
			// field. Carry the same value in internal metadata until generated
			// artifacts are refreshed, while the new field remains the primary
			// contract once it is available.
			callContext = metadata.AppendToOutgoingContext(callContext, "seev-fee-quote-id", request.FeeQuoteID)
		}
		var headers metadata.MD
		result, err := client.CreateTopupIntent(callContext, protoRequest, grpc.Header(&headers))
		if err != nil {
			switch status.Code(err) {
			case codes.InvalidArgument:
				message := status.Convert(err).Message()
				if strings.HasPrefix(message, "CURRENCY_INVALID") {
					response.ErrorStatus(w, http.StatusBadRequest, "CURRENCY_INVALID", message)
				} else {
					response.BadRequest(w, message)
				}
			case codes.FailedPrecondition:
				if writeLedgerBusinessError(w, err) {
					return
				}
				message := status.Convert(err).Message()
				switch {
				case message == "TOPUP_FEE_QUOTE_REQUIRED":
					response.JSON(w, http.StatusUnprocessableEntity, response.Envelope{Success: false, Error: &response.Error{Code: "TOPUP_FEE_QUOTE_REQUIRED", Message: "a valid fee quote is required"}})
				case message == "INTAKE_PAUSED":
					response.JSON(w, http.StatusUnprocessableEntity, response.Envelope{Success: false, Error: &response.Error{Code: "INTAKE_PAUSED", Message: "top-up intake is paused"}})
				case strings.HasPrefix(message, "["):
					response.UnprocessableEntity(w, message)
				default:
					response.JSON(w, http.StatusUnprocessableEntity, response.Envelope{Success: false, Error: &response.Error{Code: "NO_ROUTE", Message: "no topup route available"}})
				}
			case codes.Unavailable:
				// docs/roadmap/archive/40 Task T2 — every candidate vendor is
				// registered but circuit-broken; distinct from NO_ROUTE
				// (a config problem) since this is transient.
				response.JSON(w, http.StatusServiceUnavailable, response.Envelope{Success: false, Error: &response.Error{Code: "VENDOR_UNAVAILABLE", Message: "no vendor available"}})
			default:
				response.InternalServerError(w, err)
			}
			return
		}
		body := topupFromProto(result.GetIntent())
		applyFeeSnapshotHeaders(&body, headers)
		response.Created(w, body)
	}
}

func protoStringField(message interface{ ProtoReflect() protoreflect.Message }, name string) string {
	field := message.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || field.Kind() != protoreflect.StringKind {
		return ""
	}
	return message.ProtoReflect().Get(field).String()
}

func setProtoStringField(message interface{ ProtoReflect() protoreflect.Message }, name, value string) {
	if value == "" {
		return
	}
	field := message.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || field.Kind() != protoreflect.StringKind {
		return
	}
	message.ProtoReflect().Set(field, protoreflect.ValueOfString(value))
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
		var headers metadata.MD
		result, err := client.GetTopupIntent(r.Context(), &payinv1.GetTopupIntentRequest{Id: id.String(), UserId: userID.String()}, grpc.Header(&headers))
		if err != nil {
			if status.Code(err) == codes.NotFound {
				response.NotFound(w, "topup intent not found")
			} else {
				response.InternalServerError(w, err)
			}
			return
		}
		body := topupFromProto(result.GetIntent())
		applyFeeSnapshotHeaders(&body, headers)
		response.OK(w, body)
	}
}

func applyFeeSnapshotHeaders(response *topupResponse, headers metadata.MD) {
	first := func(key string) string {
		values := headers.Get(key)
		if len(values) == 0 {
			return ""
		}
		return values[0]
	}
	if response.FeeAmount == "" {
		response.FeeAmount = first("seev-fee-amount")
	}
	if response.TotalDebit == "" {
		response.TotalDebit = first("seev-total-debit")
	}
	if response.FeeGateway == "" {
		response.FeeGateway = first("seev-fee-gateway")
	}
	if response.FeeQuoteID == "" {
		response.FeeQuoteID = first("seev-fee-quote-id")
	}
	if response.FeeApplication == "" {
		response.FeeApplication = first("seev-fee-application")
	}
}

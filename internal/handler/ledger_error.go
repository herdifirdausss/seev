package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/herdifirdausss/seev/pkg/ledgererr"
	"github.com/herdifirdausss/seev/pkg/response"
	"google.golang.org/grpc/status"
)

// writeLedgerBusinessError preserves Ledger's stable business code when an
// owner service has returned it over gRPC. Older owner handlers encode the
// code in the legacy "[CODE] message" form, while newer ones attach ErrorInfo;
// accept both during the additive contract rollout.
func writeLedgerBusinessError(w http.ResponseWriter, err error) bool {
	mapped := ledgererr.FromStatus(err)
	var business *ledgererr.LedgerError
	if errors.As(mapped, &business) {
		response.ErrorStatus(w, http.StatusUnprocessableEntity, business.Code, business.Message)
		return true
	}
	message := status.Convert(err).Message()
	if !strings.HasPrefix(message, "[") {
		return false
	}
	close := strings.IndexByte(message, ']')
	if close <= 1 || close+1 >= len(message) {
		return false
	}
	code := message[1:close]
	body := strings.TrimSpace(message[close+1:])
	if body == "" {
		body = code
	}
	response.ErrorStatus(w, http.StatusUnprocessableEntity, code, body)
	return true
}

package grpcserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/internal/vendorgw"
)

type fakeService struct {
	outcome string
	err     error
	vendor  string
	headers http.Header
	body    []byte
}

func (f *fakeService) HandleWebhookResult(_ context.Context, vendor string, headers http.Header, body []byte) (string, error) {
	f.vendor, f.headers, f.body = vendor, headers, append([]byte(nil), body...)
	return f.outcome, f.err
}

func (*fakeService) CreateTopupIntent(context.Context, uuid.UUID, decimal.Decimal) (model.TopupIntent, error) {
	return model.TopupIntent{}, nil
}

func (*fakeService) GetTopupIntent(context.Context, uuid.UUID) (model.TopupIntent, error) {
	return model.TopupIntent{}, nil
}

func startServer(t *testing.T, service Service) payinv1.PayinServiceClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	payinv1.RegisterPayinServiceServer(server, New(service, errors.New("not found"), errors.New("no route"), errors.New("no vendor available")))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	connection, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	return payinv1.NewPayinServiceClient(connection)
}

func TestHandleWebhook_AllOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		outcome    string
		err        error
		wantCode   codes.Code
		wantResult payinv1.WebhookResult
	}{
		{name: "ok", outcome: "ok", wantCode: codes.OK, wantResult: payinv1.WebhookResult_WEBHOOK_RESULT_OK},
		{name: "ignored", outcome: "ignored", wantCode: codes.OK, wantResult: payinv1.WebhookResult_WEBHOOK_RESULT_IGNORED},
		{name: "unknown vendor", err: vendorgw.ErrUnknownPayinVendor, wantCode: codes.NotFound},
		{name: "bad signature", err: vendorgw.ErrInvalidSignature, wantCode: codes.Unauthenticated},
		{name: "business failure", outcome: "business_failure", err: errors.New("account suspended"), wantCode: codes.OK, wantResult: payinv1.WebhookResult_WEBHOOK_RESULT_BUSINESS_FAILURE},
		{name: "infrastructure failure", err: errors.New("database unavailable"), wantCode: codes.Internal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeService{outcome: test.outcome, err: test.err}
			client := startServer(t, service)
			raw := []byte("\x00exact\nraw-body")
			response, err := client.HandleWebhook(context.Background(), &payinv1.HandleWebhookRequest{
				Vendor: "mockvendor", Headers: map[string]string{"X-Signature": "abc"}, RawBody: raw,
			})
			assert.Equal(t, test.wantCode, status.Code(err))
			if test.wantCode == codes.OK {
				require.NoError(t, err)
				assert.Equal(t, test.wantResult, response.GetResult())
			}
			assert.Equal(t, "mockvendor", service.vendor)
			assert.Equal(t, "abc", service.headers.Get("X-Signature"))
			assert.Equal(t, raw, service.body, "raw webhook bytes must cross the wire unchanged")
		})
	}
}

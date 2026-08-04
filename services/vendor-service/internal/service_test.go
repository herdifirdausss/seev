package vendorboundary

import (
	"context"
	"testing"

	vendorv1 "github.com/herdifirdausss/seev/gen/go/vendorservice/v1"
)

type adapterStub struct{}

func (adapterStub) CreatePayinSession(context.Context, *vendorv1.CreatePayinSessionRequest) (*vendorv1.CreatePayinSessionResponse, error) {
	return &vendorv1.CreatePayinSessionResponse{Status: "pending"}, nil
}
func (adapterStub) SubmitPayout(context.Context, *vendorv1.SubmitPayoutRequest) (*vendorv1.PayoutResult, error) {
	return &vendorv1.PayoutResult{Status: "pending"}, nil
}
func (adapterStub) QueryPayout(context.Context, *vendorv1.QueryPayoutRequest) (*vendorv1.PayoutResult, error) {
	return &vendorv1.PayoutResult{Status: "pending"}, nil
}

func TestRegistryRejectsDuplicateAndUnknownAdapter(t *testing.T) {
	r := NewRegistry()
	if err := r.Add("mockvendor", adapterStub{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Add("mockvendor", adapterStub{}); err == nil {
		t.Fatal("duplicate adapter accepted")
	}
	s := NewServer(r)
	if _, err := s.SubmitPayout(context.Background(), &vendorv1.SubmitPayoutRequest{Vendor: "missing", RequestId: "r", Amount: "1", Currency: "IDR", Destination: []byte("x")}); err == nil {
		t.Fatal("unknown vendor accepted")
	}
}

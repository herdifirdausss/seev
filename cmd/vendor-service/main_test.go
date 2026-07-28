package main

import (
	"net/http"
	"testing"

	"github.com/herdifirdausss/seev/pkg/httpcontract"
)

func TestVendorHTTPHandlerUsesContractMetadata(t *testing.T) {
	handler := vendorHTTPHandler(nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	mux, ok := handler.(*httpcontract.Mux)
	if !ok {
		t.Fatalf("vendor HTTP handler must be an httpcontract.Mux, got %T", handler)
	}
	if err := httpcontract.Validate(mux.Snapshot()); err != nil {
		t.Fatal(err)
	}
	registrations := mux.Snapshot()
	if len(registrations) != 4 {
		t.Fatalf("expected four VendorService HTTP registrations, got %d", len(registrations))
	}
	for _, registration := range registrations {
		if registration.Owner != "vendor" || registration.Contract != "webhooks-v1" {
			t.Errorf("unexpected VendorService registration metadata: %#v", registration)
		}
	}
}

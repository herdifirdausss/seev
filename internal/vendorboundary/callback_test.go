package vendorboundary

import (
	"net/http/httptest"
	"testing"
)

func TestCallbackSourcePolicyRequiresAllowlistedPeer(t *testing.T) {
	handler, err := NewCallbackHandler(nil, NewRegistry(), nil, nil, "127.0.0.1/32", "10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/webhooks/mockvendor", nil)
	request.RemoteAddr = "192.0.2.10:443"
	if handler.sourceAllowed(request) {
		t.Fatal("unexpectedly accepted non-allowlisted peer")
	}
	request.RemoteAddr = "127.0.0.1:443"
	if !handler.sourceAllowed(request) {
		t.Fatal("expected loopback peer to be accepted")
	}
}

func TestCallbackSourcePolicyUsesForwardedIPOnlyFromTrustedProxy(t *testing.T) {
	handler, err := NewCallbackHandler(nil, NewRegistry(), nil, nil, "203.0.113.0/24", "10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/webhooks/mockvendor", nil)
	request.RemoteAddr = "10.0.0.4:443"
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	if !handler.sourceAllowed(request) {
		t.Fatal("expected trusted proxy forwarded IP to be accepted")
	}
	request.RemoteAddr = "192.0.2.10:443"
	if handler.sourceAllowed(request) {
		t.Fatal("untrusted peer must not control forwarded IP")
	}
}

func TestCallbackOutcomeMapping(t *testing.T) {
	for input, want := range map[string]string{
		"VENDOR_CALLBACK_RESULT_FINALIZED":            "finalized",
		"VENDOR_CALLBACK_RESULT_ALREADY_FINALIZED":    "finalized",
		"VENDOR_CALLBACK_RESULT_IGNORED_NON_TERMINAL": "ignored",
		"VENDOR_CALLBACK_RESULT_RECORDED_UNMATCHED":   "unmatched",
	} {
		status, _ := callbackOutcome(input)
		if status != want {
			t.Fatalf("callbackOutcome(%q) = %q, want %q", input, status, want)
		}
	}
}

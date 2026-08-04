package adminbff

import "testing"

// TestRewriteProxyPath_LedgerAdminCatchAll proves the routing-bug fix (a
// load-testing session's finding): "/api/v1/admin/ledger/*" must rewrite to
// "/api/v1/ledger/admin/*" — the one downstream mount
// (services/ledger/cmd/ledger/main.go) that actually strips cleanly to
// ledger-service's own "/admin/*" route table. Before this fix,
// downstreamPrefix equaled publicPrefix (no rewrite at all), so the request
// path reached ledger-service unchanged and hit a mount with no possible
// correct StripPrefix length — every disbursement/savings/schedule request
// through this catch-all 404d.
func TestRewriteProxyPath_LedgerAdminCatchAll(t *testing.T) {
	got := rewriteProxyPath("/api/v1/admin/ledger/disbursements", "", "/api/v1/admin/ledger/", "/api/v1/ledger/admin/")
	want := "/api/v1/ledger/admin/disbursements"
	if got != want {
		t.Fatalf("rewriteProxyPath() = %q, want %q", got, want)
	}
}

func TestRewriteProxyPath_PreservesQueryString(t *testing.T) {
	got := rewriteProxyPath("/api/v1/admin/ledger/disbursements/abc/run", "retry_failed=true", "/api/v1/admin/ledger/", "/api/v1/ledger/admin/")
	want := "/api/v1/ledger/admin/disbursements/abc/run?retry_failed=true"
	if got != want {
		t.Fatalf("rewriteProxyPath() = %q, want %q", got, want)
	}
}

// TestRewriteProxyPath_AdjustmentsAndRecon proves the two routes this
// session's fix was modeled on keep working unchanged.
func TestRewriteProxyPath_AdjustmentsAndRecon(t *testing.T) {
	cases := []struct {
		name             string
		requestPath      string
		publicPrefix     string
		downstreamPrefix string
		want             string
	}{
		{"adjustments", "/api/v1/admin/adjustments/xyz", "/api/v1/admin/adjustments/", "/api/v1/ledger/admin/adjustments/", "/api/v1/ledger/admin/adjustments/xyz"},
		{"recon", "/api/v1/admin/recon/batches/1", "/api/v1/admin/recon/", "/api/v1/ledger/admin/recon/", "/api/v1/ledger/admin/recon/batches/1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rewriteProxyPath(tc.requestPath, "", tc.publicPrefix, tc.downstreamPrefix)
			if got != tc.want {
				t.Fatalf("rewriteProxyPath() = %q, want %q", got, tc.want)
			}
		})
	}
}

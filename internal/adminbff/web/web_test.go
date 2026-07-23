package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderOperationsPages(t *testing.T) {
	for _, page := range []string{"dashboard", "maker", "payout", "recon"} {
		t.Run(page, func(t *testing.T) {
			response := httptest.NewRecorder()
			err := Render(response, page, PageData{Title: page, CSRFToken: "csrf", Role: "admin_maker", IsMaker: true})
			if err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d", response.Code)
			}
			body := response.Body.String()
			for _, want := range []string{"/assets/pico.min.css", "/assets/htmx.min.js", "csrf"} {
				if !strings.Contains(body, want) {
					t.Fatalf("page %s missing %q", page, want)
				}
			}
		})
	}
}

func TestAssetHandlerServesVendoredFiles(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/htmx.min.js", nil)
	response := httptest.NewRecorder()
	AssetHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "htmx") {
		t.Fatalf("asset status=%d body prefix=%q", response.Code, response.Body.String()[:min(32, response.Body.Len())])
	}
}

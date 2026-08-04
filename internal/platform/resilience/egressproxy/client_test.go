package egressproxy

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestNewClientRequiredWithoutProxyFailsClosed(t *testing.T) {
	_, err := NewClient("", true, time.Second)
	if !errors.Is(err, ErrRequired) {
		t.Fatalf("error = %v, want ErrRequired", err)
	}
}

func TestNewClientBindsExplicitProxy(t *testing.T) {
	client, err := NewClient("http://proxy.example.test:3128", true, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy == nil {
		t.Fatal("expected an explicit proxy transport")
	}
	request, err := http.NewRequest(http.MethodGet, "https://vendor.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	proxyURL, err := transport.Proxy(request)
	if err != nil {
		t.Fatal(err)
	}
	if proxyURL.Host != "proxy.example.test:3128" {
		t.Fatalf("proxy = %s, want proxy.example.test:3128", proxyURL.Host)
	}
}

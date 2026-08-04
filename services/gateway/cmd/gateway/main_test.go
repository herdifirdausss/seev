package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	require.NoError(t, err)
	assert.NoError(t, probeHealth(func(key string) string {
		if key == "APP_PORT" {
			return parsed.Port()
		}
		return ""
	}))
}

func TestProbeHealthRejectsUnhealthyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	require.NoError(t, err)
	err = probeHealth(func(key string) string {
		if key == "APP_PORT" {
			return parsed.Port()
		}
		return ""
	})
	assert.ErrorContains(t, err, "503")
}

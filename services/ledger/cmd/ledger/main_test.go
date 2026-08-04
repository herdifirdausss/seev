package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

type fakeDB struct{ err error }

func (f fakeDB) HealthCheck(context.Context) error { return f.err }

type fakeCache struct{ err error }

func (f fakeCache) HealthCheck(context.Context) error { return f.err }

type fakeMQ struct{ err error }

func (f fakeMQ) HealthCheck() error { return f.err }

func TestLive(t *testing.T) {
	recorder := httptest.NewRecorder()
	live(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"status":"ok"}`, recorder.Body.String())
}

func TestReady(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ready(fakeDB{}, nil, fakeMQ{})(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Contains(t, recorder.Body.String(), `"redis":"disabled"`)
	})

	t.Run("dependency failure", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ready(fakeDB{}, fakeCache{err: errors.New("redis down")}, fakeMQ{})(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))
		assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		assert.Contains(t, recorder.Body.String(), "redis down")
	})
}

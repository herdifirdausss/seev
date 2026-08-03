// Command mock-push-provider is a repository-local, deterministic push sink
// for C3 development. It has no user database and never talks to the
// internet; the Gateway adapter can use token prefixes to exercise retry and
// invalid-endpoint paths.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

type message struct {
	DeliveryID string            `json:"delivery_id"`
	Platform   string            `json:"platform"`
	Title      string            `json:"title"`
	Body       string            `json:"body"`
	Data       map[string]string `json:"data,omitempty"`
	AcceptedAt time.Time         `json:"accepted_at"`
	Duplicate  bool              `json:"duplicate,omitempty"`
}

type incoming struct {
	DeliveryID   string `json:"delivery_id"`
	Token        string `json:"token"`
	Platform     string `json:"platform"`
	Notification struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	} `json:"notification"`
	Data map[string]string `json:"data"`
}

type sink struct {
	mu       sync.Mutex
	messages map[string]message
}

func (s *sink) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.mu.Lock()
		out := make([]message, 0, len(s.messages))
		for _, item := range s.messages {
			out = append(out, item)
		}
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"messages": out})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req incoming
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if req.DeliveryID == "" || req.Token == "" || req.Platform == "" || req.Notification.Title == "" || req.Notification.Body == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message_invalid"})
		return
	}
	if strings.HasPrefix(req.Token, "invalid:") {
		writeJSON(w, http.StatusGone, map[string]string{"error": "invalid_token"})
		return
	}
	if strings.HasPrefix(req.Token, "rate:") {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
		return
	}
	if strings.HasPrefix(req.Token, "transient:") || strings.HasPrefix(req.Token, "5xx:") {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "temporary_provider_failure"})
		return
	}
	if strings.HasPrefix(req.Token, "timeout:") {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(15 * time.Second):
		}
	}
	s.mu.Lock()
	if existing, ok := s.messages[req.DeliveryID]; ok {
		existing.Duplicate = true
		s.mu.Unlock()
		w.Header().Set("X-Provider-Message-ID", providerMessageID(req.DeliveryID))
		writeJSON(w, http.StatusOK, existing)
		return
	}
	accepted := message{DeliveryID: req.DeliveryID, Platform: req.Platform, Title: req.Notification.Title, Body: req.Notification.Body, Data: req.Data, AcceptedAt: time.Now().UTC()}
	s.messages[req.DeliveryID] = accepted
	s.mu.Unlock()
	w.Header().Set("X-Provider-Message-ID", providerMessageID(req.DeliveryID))
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "message_id": providerMessageID(req.DeliveryID)})
}

func providerMessageID(deliveryID string) string {
	sum := sha256.Sum256([]byte(deliveryID))
	return "mock-" + hex.EncodeToString(sum[:8])
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func main() {
	port := flag.String("port", envOr("MOCK_PUSH_PORT", "8097"), "listen port")
	flag.Parse()
	logger := slog.Default()
	store := &sink{messages: make(map[string]message)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/v1/messages", store.handleMessage)
	server := &http.Server{Addr: ":" + *port, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("mock push provider stopped", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		logger.Error("mock push provider shutdown failed", "error", err)
	}
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

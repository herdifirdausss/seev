package webhook

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/merchant/model"
	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/pkg/cryptox"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

const (
	claimBatchSize = 50
	leaseDuration  = 2 * time.Minute

	// maxDeliveryAttempts and the backoff formula in nextAttemptAt
	// deliberately mirror
	// internal/payout/repository/vendor_command_repository.go's
	// FailCommand (itself matched to
	// internal/ledger/repository/outbox_event_repository.go's MarkFailed):
	// base 30s, factor 2, cap 15m, plus up to 50% jitter — so this
	// codebase's outbox-style retry implementations don't drift into
	// different timing philosophies for no reason. Unlike
	// payout_vendor_commands, merchant_webhook_deliveries has no per-row
	// max_retries column (T7's own migration never added one), so the cap
	// is a package constant instead.
	maxDeliveryAttempts = 15
	backoffBaseSeconds  = 30
	backoffCapSeconds   = 900

	responseExcerptLimit = 500
)

// RelayWorker is T7's leasing delivery worker — the dispatch side of this
// package (Service, in service.go, is the tenant-facing endpoint-management
// side; consumer.go is what enqueues the deliveries this worker claims).
// ClaimDue's `FOR UPDATE SKIP LOCKED` claim lets multiple RelayWorker
// instances run concurrently against the same table with no coordination
// beyond the database, and lets a crashed worker's dangling lease be
// reclaimed by any instance once it expires — no separate recovery pass.
type RelayWorker struct {
	repo       repository.WebhookRepository
	ring       *cryptox.Ring
	logger     *slog.Logger
	instanceID string
}

// NewRelayWorker panics on a nil repo/ring — same construct-now posture as
// every other component in this package (NewService).
func NewRelayWorker(repo repository.WebhookRepository, ring *cryptox.Ring, logger *slog.Logger) *RelayWorker {
	if repo == nil {
		panic("merchant/webhook: NewRelayWorker requires a non-nil WebhookRepository")
	}
	if ring == nil {
		panic("merchant/webhook: NewRelayWorker requires a non-nil cryptox ring")
	}
	if logger == nil {
		logger = slog.Default()
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "worker"
	}
	return &RelayWorker{repo: repo, ring: ring, logger: logger, instanceID: host + "-" + uuid.NewString()[:8]}
}

// Start launches a background poll loop calling ProcessOnce every
// interval — matches pkg/objectoutbox.Worker.Start's own Start/Stop
// lifecycle shape. Call the returned stop func to cancel and wait for the
// in-flight batch to finish.
func (w *RelayWorker) Start(ctx context.Context, interval time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, _, err := w.ProcessOnce(ctx); err != nil {
					w.logger.Error("merchant/webhook: process once failed", "error", err)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// ProcessOnce claims up to claimBatchSize due deliveries and attempts each
// exactly once.
func (w *RelayWorker) ProcessOnce(ctx context.Context) (processed, failed int, err error) {
	deliveries, err := w.repo.ClaimDue(ctx, claimBatchSize, w.instanceID, time.Now().Add(leaseDuration))
	if err != nil {
		return 0, 0, fmt.Errorf("merchant/webhook: claim due deliveries: %w", err)
	}
	for _, d := range deliveries {
		if procErr := w.processDelivery(ctx, d); procErr != nil {
			failed++
			w.logger.Error("merchant/webhook: process delivery failed", "delivery_id", d.ID, "error", procErr)
			continue
		}
		processed++
	}
	return processed, failed, nil
}

// processDelivery dispatches one claimed delivery and persists its
// outcome. A returned error means BOOKKEEPING itself failed (a DB write
// after the HTTP attempt) — the delivery is left leased and will be
// reclaimed once the lease expires. It never means the HTTP attempt
// failed: that outcome (2xx / non-2xx / transport error / SSRF rejection)
// is handled and persisted internally as delivered/failed/dead, not
// surfaced as a Go error here.
func (w *RelayWorker) processDelivery(ctx context.Context, d model.WebhookDelivery) error {
	endpoint, err := w.repo.GetEndpoint(ctx, d.TenantID, d.EndpointID)
	if err != nil {
		return fmt.Errorf("get endpoint: %w", err)
	}
	if endpoint.Status != "enabled" {
		// Disabled after this delivery was queued (manually, or by an
		// earlier attempt's own 410) — nothing left to deliver to.
		return w.repo.MarkDead(ctx, d.ID)
	}

	event, err := w.repo.GetEventByID(ctx, d.EventID)
	if err != nil {
		return fmt.Errorf("get event: %w", err)
	}

	secret, err := w.ring.Open(secretAAD(endpoint.ID), endpoint.SecretCiphertext)
	if err != nil {
		return fmt.Errorf("open endpoint secret: %w", err)
	}

	// t is derived from the delivery row's own immutable CreatedAt, never
	// recomputed per attempt — the LOCKED signature scheme (signature.go)
	// requires every attempt of one delivery to sign the SAME t. CreatedAt
	// is set once at INSERT and never updated, so reusing it structurally
	// guarantees that without a dedicated column.
	t := d.CreatedAt.Unix()
	sig := Sign(secret, t, event.PayloadBytes)

	attemptNumber := d.AttemptCount + 1
	startedAt := time.Now()
	status, excerpt, dispatchErr := dispatch(ctx, endpoint, sig, event.PayloadBytes)
	finishedAt := time.Now()

	attempt := model.WebhookAttempt{
		ID: generalutil.NewV7(), DeliveryID: d.ID, AttemptNumber: attemptNumber,
		StartedAt: startedAt, FinishedAt: finishedAt, DurationMS: int(finishedAt.Sub(startedAt).Milliseconds()),
	}
	if dispatchErr != nil {
		errCode := "dispatch_error"
		attempt.ErrorCode = &errCode
	} else {
		httpStatus := status
		attempt.HTTPStatus = &httpStatus
		if excerpt != "" {
			attempt.ResponseExcerpt = &excerpt
		}
		if status < 200 || status >= 300 {
			errCode := fmt.Sprintf("http_%d", status)
			attempt.ErrorCode = &errCode
		}
	}
	if recErr := w.repo.RecordAttempt(ctx, attempt); recErr != nil {
		return fmt.Errorf("record attempt: %w", recErr)
	}

	switch {
	case dispatchErr == nil && status >= 200 && status < 300:
		deliveryAttemptsTotal.WithLabelValues("delivered").Inc()
		return w.repo.MarkDelivered(ctx, d.ID, status)

	case dispatchErr == nil && status == http.StatusGone:
		// TM-16 / T7 acceptance: auto-disable on 410 — the receiver told us
		// this endpoint is permanently gone, so both the endpoint and this
		// delivery stop here rather than exhausting the retry schedule.
		if disableErr := w.repo.DisableEndpoint(ctx, endpoint.ID); disableErr != nil {
			w.logger.Error("merchant/webhook: disable endpoint on 410 failed", "endpoint_id", endpoint.ID, "error", disableErr)
		}
		deliveryAttemptsTotal.WithLabelValues("dead").Inc()
		return w.repo.MarkDead(ctx, d.ID)

	case attemptNumber >= maxDeliveryAttempts:
		deliveryAttemptsTotal.WithLabelValues("dead").Inc()
		return w.repo.MarkDead(ctx, d.ID)

	default:
		errorCode := "dispatch_error"
		var httpStatus *int
		if dispatchErr == nil {
			errorCode = fmt.Sprintf("http_%d", status)
			s := status
			httpStatus = &s
		}
		deliveryAttemptsTotal.WithLabelValues("failed").Inc()
		return w.repo.MarkFailedAttempt(ctx, d.ID, errorCode, httpStatus, nextAttemptAt(attemptNumber))
	}
}

// dispatch performs one HTTP delivery attempt. A non-nil error means the
// request never produced an HTTP response at all (dial/timeout/transport
// failure, or the SSRF guard in ssrf.go refusing to dial); a nil error
// with any status code (including non-2xx) means the endpoint was reached
// and responded — safeClient's CheckRedirect (ssrf.go) means a 3xx is
// returned here too, never followed.
func dispatch(ctx context.Context, endpoint model.WebhookEndpoint, signature string, body []byte) (status int, responseExcerpt string, err error) {
	req, err := http.NewRequestWithContext(ctx, deliveryHTTPMethod, endpoint.URL, bytes.NewReader(body))
	if err != nil {
		return 0, "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SignatureHeader, signature)

	resp, err := safeClient(endpoint.Environment).Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	excerpt := string(raw)
	if len(excerpt) > responseExcerptLimit {
		excerpt = excerpt[:responseExcerptLimit]
	}
	return resp.StatusCode, excerpt, nil
}

// nextAttemptAt computes the retry time after a delivery's attemptNumber'th
// attempt has just failed — identical formula to
// internal/payout/repository/vendor_command_repository.go's FailCommand.
func nextAttemptAt(attemptNumber int) time.Time {
	backoff := float64(backoffBaseSeconds) * math.Pow(2, float64(attemptNumber))
	if backoff > backoffCapSeconds {
		backoff = backoffCapSeconds
	}
	backoff *= 1 + rand.Float64()*0.5 //nolint:gosec // jitter timing, not security-sensitive.
	return time.Now().Add(time.Duration(backoff) * time.Second)
}

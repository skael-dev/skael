package webhooks

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/riverqueue/river"
)

// maxAttempts and maxBackoff make about 24 attempts in about 24 hours. River's
// default of about three weeks is too long for a consumer waiting on a change.
const (
	maxAttempts = 24
	maxBackoff  = 80 * time.Minute
)

// DeliveryArgs holds an id and nothing else. River UI shows arguments to
// admins, and a payload or a secret must not appear there.
type DeliveryArgs struct {
	DeliveryID string `json:"delivery_id"`
}

// Kind implements river.JobArgs.
func (DeliveryArgs) Kind() string { return "webhook_delivery" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (DeliveryArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: maxAttempts, Queue: river.QueueDefault}
}

// DeliveryWorker sends one attempt of one delivery.
type DeliveryWorker struct {
	river.WorkerDefaults[DeliveryArgs]
	store  *Store
	client *http.Client
}

// NewDeliveryWorker constructs the worker.
func NewDeliveryWorker(store *Store, allowPrivate func() bool) *DeliveryWorker {
	return &DeliveryWorker{store: store, client: NewLiveClient(allowPrivate)}
}

// NextRetry doubles from one minute up to maxBackoff.
func (w *DeliveryWorker) NextRetry(job *river.Job[DeliveryArgs]) time.Time {
	return time.Now().Add(backoff(job.Attempt))
}

func backoff(attempt int) time.Duration {
	wait := time.Minute
	for i := 1; i < attempt && wait < maxBackoff; i++ {
		wait *= 2
	}
	return min(wait, maxBackoff)
}

// Timeout bounds one attempt beyond the HTTP client's own limit.
func (w *DeliveryWorker) Timeout(*river.Job[DeliveryArgs]) time.Duration { return 30 * time.Second }

// Work sends the event. Returning an error makes River retry, and the state
// becomes failed only on the attempt River will not repeat.
func (w *DeliveryWorker) Work(ctx context.Context, job *river.Job[DeliveryArgs]) error {
	d, err := w.store.loadDelivery(ctx, job.Args.DeliveryID)
	if err != nil {
		return err
	}
	if d == nil {
		return nil
	}
	// A disabled endpoint receives nothing. The delivery ends as failed, so it
	// does not sit pending forever and can be redelivered after a re-enable.
	if !d.endpoint.Enabled {
		return w.store.recordAttempt(ctx, job.Args.DeliveryID, nil, "the endpoint is disabled", false, "failed")
	}
	code, sendErr := w.send(ctx, d)
	if sendErr == nil {
		return w.store.recordAttempt(ctx, job.Args.DeliveryID, code, "", true, "delivered")
	}
	state := "pending"
	if job.Attempt >= job.MaxAttempts {
		state = "failed"
	}
	if err := w.store.recordAttempt(ctx, job.Args.DeliveryID, code, sendErr.Error(), false, state); err != nil {
		return err
	}
	return sendErr
}

func (w *DeliveryWorker) send(ctx context.Context, d *delivery) (*int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint.URL, bytes.NewReader(d.body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	now := time.Now().Unix()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Skael-Webhook/1")
	req.Header.Set(HeaderEventID, d.eventID)
	req.Header.Set(HeaderEventType, d.event)
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(now, 10))
	req.Header.Set(HeaderSignature, SignatureHeader(now, d.body, d.endpoint.secret, d.endpoint.previousSecret))

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.CopyN(io.Discard, resp.Body, 4096)
	code := resp.StatusCode
	if code < 200 || code >= 300 {
		return &code, fmt.Errorf("receiver returned %d", code)
	}
	return &code, nil
}

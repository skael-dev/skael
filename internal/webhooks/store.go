// Package webhooks sends signed events to endpoints that owners register.
package webhooks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The event types.
const (
	EventVersionPublished = "skill.version.published"
	EventVersionApproved  = "skill.version.approved"
	EventSetChanged       = "set.changed"
	EventSubmissionDone   = "submission.decided"
	eventTest             = "webhook.test"
)

// EventTypes lists what an endpoint can subscribe to.
var EventTypes = []string{EventVersionPublished, EventVersionApproved, EventSetChanged, EventSubmissionDone}

// Endpoint is a registered receiver. Its secrets never leave through this type.
type Endpoint struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	EventTypes []string  `json:"event_types"`
	SetIDs     []string  `json:"set_ids"`
	Enabled    bool      `json:"enabled"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`

	PreviousExpiresAt *time.Time `json:"previous_secret_expires_at,omitempty"`

	secret, previousSecret string
}

// Attempt is one try at a delivery.
type Attempt struct {
	ResponseCode *int      `json:"response_code"`
	Error        string    `json:"error,omitempty"`
	Succeeded    bool      `json:"succeeded"`
	CreatedAt    time.Time `json:"created_at"`
}

// Delivery is one event for one endpoint, with every attempt.
type Delivery struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	EventType string    `json:"event_type"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Attempts  []Attempt `json:"attempts"`
}

// Store persists endpoints and the delivery log.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const endpointColumns = `id, name, url, event_types, set_ids::text[], enabled, created_by, created_at, previous_expires_at, secret, previous_secret`

func scanEndpoint(row pgx.Row) (*Endpoint, error) {
	var e Endpoint
	err := row.Scan(&e.ID, &e.Name, &e.URL, &e.EventTypes, &e.SetIDs, &e.Enabled, &e.CreatedBy, &e.CreatedAt, &e.PreviousExpiresAt, &e.secret, &e.previousSecret)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("webhooks: scan endpoint: %w", err)
	}
	return &e, nil
}

// List returns every endpoint.
func (s *Store) List(ctx context.Context) ([]Endpoint, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+endpointColumns+` FROM webhook_endpoints ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("webhooks.List: %w", err)
	}
	defer rows.Close()
	out := []Endpoint{}
	for rows.Next() {
		e, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// Get returns an endpoint, or nil.
func (s *Store) Get(ctx context.Context, id string) (*Endpoint, error) {
	return scanEndpoint(s.pool.QueryRow(ctx, `SELECT `+endpointColumns+` FROM webhook_endpoints WHERE id = $1`, id))
}

// Rotate replaces the secret and keeps the old one valid for the window.
func (s *Store) Rotate(ctx context.Context, id, secret string, window time.Duration) (*Endpoint, error) {
	return scanEndpoint(s.pool.QueryRow(ctx, `
		UPDATE webhook_endpoints
		SET previous_secret = secret, secret = $2, previous_expires_at = now() + make_interval(secs => $3)
		WHERE id = $1 RETURNING `+endpointColumns, id, secret, window.Seconds()))
}

// Deliveries lists an endpoint's newest deliveries with their attempts.
func (s *Store) Deliveries(ctx context.Context, endpointID string, limit int) ([]Delivery, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.event_id, e.type, d.state, d.created_at, d.updated_at
		FROM webhook_deliveries d JOIN webhook_events e ON e.id = d.event_id
		WHERE d.endpoint_id = $1 ORDER BY d.created_at DESC LIMIT $2`, endpointID, limit)
	if err != nil {
		return nil, fmt.Errorf("webhooks.Deliveries: %w", err)
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		d := Delivery{Attempts: []Attempt{}}
		if err := rows.Scan(&d.ID, &d.EventID, &d.EventType, &d.State, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("webhooks.Deliveries: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("webhooks.Deliveries: %w", err)
	}
	for i := range out {
		attempts, err := s.pool.Query(ctx, `
			SELECT response_code, error, succeeded, created_at FROM webhook_attempts
			WHERE delivery_id = $1 ORDER BY created_at`, out[i].ID)
		if err != nil {
			return nil, fmt.Errorf("webhooks.Deliveries: attempts: %w", err)
		}
		list, err := pgx.CollectRows(attempts, pgx.RowToStructByPos[Attempt])
		if err != nil {
			return nil, fmt.Errorf("webhooks.Deliveries: attempts: %w", err)
		}
		if list != nil {
			out[i].Attempts = list
		}
	}
	return out, nil
}

// delivery is what a worker needs to send one attempt.
type delivery struct {
	endpoint Endpoint
	eventID  string
	event    string
	body     []byte
	state    string
}

func (s *Store) loadDelivery(ctx context.Context, id string) (*delivery, error) {
	var d delivery
	var previousSecret string
	var previousExpires *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT e.id, e.url, e.enabled, e.secret, e.previous_secret, e.previous_expires_at, ev.id, ev.type, ev.body::text, d.state
		FROM webhook_deliveries d
		JOIN webhook_endpoints e ON e.id = d.endpoint_id
		JOIN webhook_events ev ON ev.id = d.event_id
		WHERE d.id = $1`, id).Scan(&d.endpoint.ID, &d.endpoint.URL, &d.endpoint.Enabled, &d.endpoint.secret,
		&previousSecret, &previousExpires, &d.eventID, &d.event, &d.body, &d.state)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("webhooks: load delivery: %w", err)
	}
	if previousExpires != nil && time.Now().Before(*previousExpires) {
		d.endpoint.previousSecret = previousSecret
	}
	return &d, nil
}

func (s *Store) recordAttempt(ctx context.Context, id string, code *int, errText string, succeeded bool, state string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("webhooks: record attempt: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `INSERT INTO webhook_attempts (delivery_id, response_code, error, succeeded) VALUES ($1, $2, $3, $4)`, id, code, errText, succeeded); err != nil {
		return fmt.Errorf("webhooks: record attempt: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE webhook_deliveries SET state = $2, updated_at = now() WHERE id = $1`, id, state); err != nil {
		return fmt.Errorf("webhooks: record attempt: %w", err)
	}
	return tx.Commit(ctx)
}

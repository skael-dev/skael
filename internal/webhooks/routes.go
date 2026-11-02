package webhooks

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/auth"
)

const defaultRotationWindow = 24 * time.Hour

// WebhookEndpointWithSecret returns the secret once, at create and at rotate.
type WebhookEndpointWithSecret struct {
	Endpoint
	Secret string `json:"secret" doc:"Shown once."`
}

func requirePrivileged(ctx context.Context) (*auth.User, error) {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return nil, huma.Error401Unauthorized("not authenticated")
	}
	if !u.IsPrivileged() {
		return nil, huma.Error403Forbidden("owner or admin role required")
	}
	return u, nil
}

func validateEventTypes(types []string) error {
	if len(types) == 0 {
		return huma.Error422UnprocessableEntity("an endpoint needs at least one event type")
	}
	for _, t := range types {
		if !slices.Contains(EventTypes, t) {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("unknown event type %q", t))
		}
	}
	return nil
}

func validateSets(ctx context.Context, pool *pgxpool.Pool, ids []string) error {
	for _, id := range ids {
		exists := false
		if uuid.Validate(id) == nil {
			if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sets WHERE id = $1)`, id).Scan(&exists); err != nil {
				return fmt.Errorf("webhooks: check set: %w", err)
			}
		}
		if !exists {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("unknown set id %q", id))
		}
	}
	return nil
}

func validateTarget(ctx context.Context, raw string, allowPrivate bool) error {
	if err := ValidateURL(ctx, raw, allowPrivate); err != nil {
		if errors.Is(err, ErrRefusedAddress) {
			return huma.Error422UnprocessableEntity(err.Error())
		}
		return err
	}
	return nil
}

// RegisterRoutes wires endpoint management. Only instance owners and admins
// reach it, because an endpoint receives data about every matching skill.
func RegisterRoutes(api huma.API, pool *pgxpool.Pool, store *Store, notifier *Notifier, allowPrivate bool) {
	type webhookEndpointsBody struct {
		Endpoints []Endpoint `json:"endpoints"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-webhook-endpoints", Method: http.MethodGet, Path: "/api/webhooks/endpoints",
		Summary: "List webhook endpoints",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body webhookEndpointsBody }, error) {
		if _, err := requirePrivileged(ctx); err != nil {
			return nil, err
		}
		list, err := store.List(ctx)
		if err != nil {
			return nil, err
		}
		return &struct{ Body webhookEndpointsBody }{Body: webhookEndpointsBody{Endpoints: list}}, nil
	})

	type webhookEndpointBody struct {
		Name       string   `json:"name" minLength:"1" maxLength:"128"`
		URL        string   `json:"url" minLength:"1" maxLength:"2048"`
		EventTypes []string `json:"event_types"`
		SetIDs     []string `json:"set_ids,omitempty" doc:"Limit set.changed to these sets. Empty means all sets."`
		Enabled    *bool    `json:"enabled,omitempty"`
	}
	validateBody := func(ctx context.Context, b webhookEndpointBody) error {
		if err := validateEventTypes(b.EventTypes); err != nil {
			return err
		}
		if err := validateSets(ctx, pool, b.SetIDs); err != nil {
			return err
		}
		return validateTarget(ctx, b.URL, allowPrivate)
	}
	setIDs := func(ids []string) []string {
		if ids == nil {
			return []string{}
		}
		return ids
	}

	huma.Register(api, huma.Operation{
		OperationID: "create-webhook-endpoint", Method: http.MethodPost, Path: "/api/webhooks/endpoints",
		Summary: "Register a webhook endpoint", DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct{ Body webhookEndpointBody }) (*struct{ Body WebhookEndpointWithSecret }, error) {
		u, err := requirePrivileged(ctx)
		if err != nil {
			return nil, err
		}
		if err := validateBody(ctx, in.Body); err != nil {
			return nil, err
		}
		secret, err := newSecret()
		if err != nil {
			return nil, err
		}
		e, err := scanEndpoint(pool.QueryRow(ctx, `
			INSERT INTO webhook_endpoints (name, url, event_types, set_ids, secret, created_by)
			VALUES ($1, $2, $3, $4::uuid[], $5, $6) RETURNING `+endpointColumns,
			in.Body.Name, in.Body.URL, in.Body.EventTypes, setIDs(in.Body.SetIDs), secret, u.Email))
		if err != nil {
			return nil, err
		}
		if err := audit.Record(ctx, pool, u.Email, "webhook.create", "webhook:"+e.ID, map[string]any{"url": e.URL, "event_types": e.EventTypes}); err != nil {
			return nil, err
		}
		return &struct{ Body WebhookEndpointWithSecret }{Body: WebhookEndpointWithSecret{Endpoint: *e, Secret: secret}}, nil
	})

	type webhookPath struct {
		ID string `path:"id"`
	}
	load := func(ctx context.Context, id string) (*auth.User, *Endpoint, error) {
		u, err := requirePrivileged(ctx)
		if err != nil {
			return nil, nil, err
		}
		if uuid.Validate(id) != nil {
			return nil, nil, huma.Error404NotFound("endpoint not found")
		}
		e, err := store.Get(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		if e == nil {
			return nil, nil, huma.Error404NotFound("endpoint not found")
		}
		return u, e, nil
	}

	huma.Register(api, huma.Operation{
		OperationID: "get-webhook-endpoint", Method: http.MethodGet, Path: "/api/webhooks/endpoints/{id}",
		Summary: "Get a webhook endpoint",
	}, func(ctx context.Context, in *webhookPath) (*struct{ Body Endpoint }, error) {
		_, e, err := load(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return &struct{ Body Endpoint }{Body: *e}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-webhook-endpoint", Method: http.MethodPut, Path: "/api/webhooks/endpoints/{id}",
		Summary: "Replace a webhook endpoint's settings",
	}, func(ctx context.Context, in *struct {
		ID   string `path:"id"`
		Body webhookEndpointBody
	}) (*struct{ Body Endpoint }, error) {
		u, old, err := load(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if err := validateBody(ctx, in.Body); err != nil {
			return nil, err
		}
		enabled := old.Enabled
		if in.Body.Enabled != nil {
			enabled = *in.Body.Enabled
		}
		e, err := scanEndpoint(pool.QueryRow(ctx, `
			UPDATE webhook_endpoints SET name = $2, url = $3, event_types = $4, set_ids = $5::uuid[], enabled = $6
			WHERE id = $1 RETURNING `+endpointColumns,
			in.ID, in.Body.Name, in.Body.URL, in.Body.EventTypes, setIDs(in.Body.SetIDs), enabled))
		if err != nil {
			return nil, err
		}
		if err := audit.Record(ctx, pool, u.Email, "webhook.update", "webhook:"+e.ID, map[string]any{"old_url": old.URL, "url": e.URL}); err != nil {
			return nil, err
		}
		return &struct{ Body Endpoint }{Body: *e}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-webhook-endpoint", Method: http.MethodDelete, Path: "/api/webhooks/endpoints/{id}",
		Summary: "Delete a webhook endpoint and its log", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *webhookPath) (*struct{}, error) {
		u, e, err := load(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if _, err := pool.Exec(ctx, `DELETE FROM webhook_endpoints WHERE id = $1`, e.ID); err != nil {
			return nil, fmt.Errorf("delete webhook endpoint: %w", err)
		}
		return nil, audit.Record(ctx, pool, u.Email, "webhook.delete", "webhook:"+e.ID, map[string]any{"url": e.URL})
	})

	type webhookRotateBody struct {
		WindowHours *int `json:"window_hours,omitempty" minimum:"0" maximum:"168" doc:"How long the old secret stays valid. The default is 24."`
	}
	huma.Register(api, huma.Operation{
		OperationID: "rotate-webhook-secret", Method: http.MethodPost, Path: "/api/webhooks/endpoints/{id}/rotate-secret",
		Summary: "Replace a webhook endpoint's secret",
	}, func(ctx context.Context, in *struct {
		ID   string `path:"id"`
		Body webhookRotateBody
	}) (*struct{ Body WebhookEndpointWithSecret }, error) {
		u, _, err := load(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		window := defaultRotationWindow
		if in.Body.WindowHours != nil {
			window = time.Duration(*in.Body.WindowHours) * time.Hour
		}
		secret, err := newSecret()
		if err != nil {
			return nil, err
		}
		e, err := store.Rotate(ctx, in.ID, secret, window)
		if err != nil {
			return nil, err
		}
		if err := audit.Record(ctx, pool, u.Email, "webhook.rotate", "webhook:"+e.ID, map[string]any{"window": window.String()}); err != nil {
			return nil, err
		}
		return &struct{ Body WebhookEndpointWithSecret }{Body: WebhookEndpointWithSecret{Endpoint: *e, Secret: secret}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "test-webhook-endpoint", Method: http.MethodPost, Path: "/api/webhooks/endpoints/{id}/test",
		Summary: "Send a test event", DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *webhookPath) (*struct{}, error) {
		_, e, err := load(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return nil, notifier.SendTest(ctx, pool, e.ID)
	})

	type webhookDeliveriesBody struct {
		Deliveries []Delivery `json:"deliveries"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-webhook-deliveries", Method: http.MethodGet, Path: "/api/webhooks/endpoints/{id}/deliveries",
		Summary: "List an endpoint's delivery log",
	}, func(ctx context.Context, in *struct {
		ID    string `path:"id"`
		Limit int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	}) (*struct{ Body webhookDeliveriesBody }, error) {
		_, e, err := load(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		list, err := store.Deliveries(ctx, e.ID, in.Limit)
		if err != nil {
			return nil, err
		}
		return &struct{ Body webhookDeliveriesBody }{Body: webhookDeliveriesBody{Deliveries: list}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "redeliver-webhook", Method: http.MethodPost, Path: "/api/webhooks/deliveries/{id}/redeliver",
		Summary: "Send a delivery again with the same event id", DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *webhookPath) (*struct{}, error) {
		if _, err := requirePrivileged(ctx); err != nil {
			return nil, err
		}
		if uuid.Validate(in.ID) != nil {
			return nil, huma.Error404NotFound("delivery not found")
		}
		found, err := notifier.Redeliver(ctx, pool, in.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, huma.Error404NotFound("delivery not found")
		}
		return nil, nil
	})
}

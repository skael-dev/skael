package client

import (
	"net/http"
	"net/url"
	"time"
)

// WebhookEndpoint is a registered receiver, without its secret.
type WebhookEndpoint struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	EventTypes []string `json:"event_types"`
	SetIDs     []string `json:"set_ids"`
	Enabled    bool     `json:"enabled"`
}

// WebhookEndpointWithSecret is an endpoint after create or rotate, the only
// responses that hold the secret.
type WebhookEndpointWithSecret struct {
	WebhookEndpoint
	Secret            string     `json:"secret"`
	PreviousExpiresAt *time.Time `json:"previous_secret_expires_at"`
}

// WebhookEndpointInput is what create and update send. The update route
// replaces every field but enabled, which stays when omitted.
type WebhookEndpointInput struct {
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	EventTypes []string `json:"event_types"`
	SetIDs     []string `json:"set_ids,omitempty"`
	Enabled    *bool    `json:"enabled,omitempty"`
}

// WebhookAttempt is one try at a delivery.
type WebhookAttempt struct {
	ResponseCode *int      `json:"response_code"`
	Error        string    `json:"error"`
	Succeeded    bool      `json:"succeeded"`
	CreatedAt    time.Time `json:"created_at"`
}

// WebhookDelivery is one event for one endpoint, with every attempt.
type WebhookDelivery struct {
	ID        string           `json:"id"`
	EventID   string           `json:"event_id"`
	EventType string           `json:"event_type"`
	State     string           `json:"state"`
	CreatedAt time.Time        `json:"created_at"`
	Attempts  []WebhookAttempt `json:"attempts"`
}

func webhookPath(id string) string {
	return "/api/webhooks/endpoints/" + url.PathEscape(id)
}

// ListWebhookEndpoints calls GET /api/webhooks/endpoints.
func (c *Client) ListWebhookEndpoints() (*Response[struct {
	Endpoints []WebhookEndpoint `json:"endpoints"`
}], error) {
	return fetch[struct {
		Endpoints []WebhookEndpoint `json:"endpoints"`
	}](c, http.MethodGet, "/api/webhooks/endpoints", nil)
}

// GetWebhookEndpoint calls GET /api/webhooks/endpoints/{id}.
func (c *Client) GetWebhookEndpoint(id string) (*Response[WebhookEndpoint], error) {
	return fetch[WebhookEndpoint](c, http.MethodGet, webhookPath(id), nil)
}

// CreateWebhookEndpoint calls POST /api/webhooks/endpoints.
func (c *Client) CreateWebhookEndpoint(in WebhookEndpointInput) (*Response[WebhookEndpointWithSecret], error) {
	return fetch[WebhookEndpointWithSecret](c, http.MethodPost, "/api/webhooks/endpoints", in)
}

// UpdateWebhookEndpoint calls PUT /api/webhooks/endpoints/{id}.
func (c *Client) UpdateWebhookEndpoint(id string, in WebhookEndpointInput) (*Response[WebhookEndpoint], error) {
	return fetch[WebhookEndpoint](c, http.MethodPut, webhookPath(id), in)
}

// DeleteWebhookEndpoint calls DELETE /api/webhooks/endpoints/{id}.
func (c *Client) DeleteWebhookEndpoint(id string) error {
	return c.deleteAt(webhookPath(id))
}

// RotateWebhookSecret calls POST /api/webhooks/endpoints/{id}/rotate-secret. A
// nil window takes the server's default.
func (c *Client) RotateWebhookSecret(id string, windowHours *int) (*Response[WebhookEndpointWithSecret], error) {
	return fetch[WebhookEndpointWithSecret](c, http.MethodPost, webhookPath(id)+"/rotate-secret", struct {
		WindowHours *int `json:"window_hours,omitempty"`
	}{windowHours})
}

// TestWebhookEndpoint calls POST /api/webhooks/endpoints/{id}/test.
func (c *Client) TestWebhookEndpoint(id string) error {
	return c.postNoBody(webhookPath(id) + "/test")
}

// ListWebhookDeliveries calls GET /api/webhooks/endpoints/{id}/deliveries.
func (c *Client) ListWebhookDeliveries(id string) (*Response[struct {
	Deliveries []WebhookDelivery `json:"deliveries"`
}], error) {
	return fetch[struct {
		Deliveries []WebhookDelivery `json:"deliveries"`
	}](c, http.MethodGet, webhookPath(id)+"/deliveries", nil)
}

// RedeliverWebhook calls POST /api/webhooks/deliveries/{id}/redeliver.
func (c *Client) RedeliverWebhook(deliveryID string) error {
	return c.postNoBody("/api/webhooks/deliveries/" + url.PathEscape(deliveryID) + "/redeliver")
}

func (c *Client) postNoBody(path string) error {
	resp, err := c.do(http.MethodPost, path, nil, "")
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

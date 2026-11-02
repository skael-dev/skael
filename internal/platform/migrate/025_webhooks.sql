-- +goose Up
-- The resolution id last seen by a change. NULL on a set that predates this
-- column: the first check stores an id and emits nothing, because it has no
-- earlier id to differ from.
ALTER TABLE sets ADD COLUMN resolution_id TEXT;

CREATE TABLE webhook_endpoints (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                TEXT NOT NULL,
    url                 TEXT NOT NULL,
    event_types         TEXT[] NOT NULL,
    set_ids             UUID[] NOT NULL DEFAULT '{}',
    secret              TEXT NOT NULL,
    previous_secret     TEXT NOT NULL DEFAULT '',
    previous_expires_at TIMESTAMPTZ,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    created_by          TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The body is stored so each attempt and a redelivery send the same event.
CREATE TABLE webhook_events (
    id         UUID PRIMARY KEY,
    type       TEXT NOT NULL,
    body       JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- River removes its own job rows, and the UI needs a lasting log. No response
-- body is kept: a receiver's reply may hold anything.
CREATE TABLE webhook_deliveries (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    endpoint_id   UUID NOT NULL REFERENCES webhook_endpoints(id) ON DELETE CASCADE,
    event_id      UUID NOT NULL REFERENCES webhook_events(id) ON DELETE CASCADE,
    state         TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'delivered', 'failed')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_webhook_deliveries_endpoint ON webhook_deliveries (endpoint_id, created_at DESC);

CREATE TABLE webhook_attempts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    delivery_id   UUID NOT NULL REFERENCES webhook_deliveries(id) ON DELETE CASCADE,
    response_code INT,
    error         TEXT NOT NULL DEFAULT '',
    succeeded     BOOLEAN NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_webhook_attempts_delivery ON webhook_attempts (delivery_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS webhook_attempts;
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhook_events;
DROP TABLE IF EXISTS webhook_endpoints;
ALTER TABLE sets DROP COLUMN IF EXISTS resolution_id;

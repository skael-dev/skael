-- +goose Up
-- One value sealed under each master key that has sealed a secret. A canary
-- that no longer opens at start means that key is lost.
CREATE TABLE master_key_canaries (
    key_id      TEXT PRIMARY KEY,
    sealed      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE master_key_canaries;

-- +goose Up
-- The one GitHub App this instance acts as. Each secret column holds a value
-- sealed under SKAEL_MASTER_KEYS, never plaintext.
CREATE TABLE github_app (
    id              INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    app_id          BIGINT NOT NULL,
    slug            TEXT NOT NULL,
    name            TEXT NOT NULL,
    owner_login     TEXT NOT NULL,
    html_url        TEXT NOT NULL,
    web_base_url    TEXT NOT NULL,
    api_base_url    TEXT NOT NULL,
    client_id       TEXT NOT NULL,
    private_key     TEXT NOT NULL,
    webhook_secret  TEXT NOT NULL,
    client_secret   TEXT NOT NULL,
    created_by      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A manifest flow in progress. The state is the CSRF token GitHub returns
-- with the code, and it is consumed once.
CREATE TABLE github_app_manifests (
    state           TEXT PRIMARY KEY,
    web_base_url    TEXT NOT NULL,
    api_base_url    TEXT NOT NULL,
    created_by      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE github_app_manifests;
DROP TABLE github_app;

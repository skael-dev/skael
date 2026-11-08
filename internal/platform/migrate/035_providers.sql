-- +goose Up
-- A provider is one LLM backend the server names for its workers. It holds no
-- secret: credential_env is the variable on each worker that holds the key.
CREATE TABLE providers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    adapter         TEXT NOT NULL CHECK (adapter IN ('anthropic', 'gateway', 'subscription')),
    base_url        TEXT NOT NULL DEFAULT '',
    auth_style      TEXT NOT NULL DEFAULT 'x-api-key' CHECK (auth_style IN ('x-api-key', 'bearer')),
    credential_env  TEXT NOT NULL,
    updated_by      TEXT NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The judge and the panel every worker runs. members is an ordered list of
-- {"provider_id", "model"}; the first is the primary member.
CREATE TABLE eval_panel (
    id              INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    judge_provider  UUID NOT NULL REFERENCES providers(id),
    judge_model     TEXT NOT NULL,
    members         JSONB NOT NULL,
    updated_by      TEXT NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE eval_panel;
DROP TABLE providers;

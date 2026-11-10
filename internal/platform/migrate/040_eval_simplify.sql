-- +goose Up
-- A provider now holds its own key, sealed under the master key or as an
-- env:/file: reference, and has a type in place of an adapter. Workers no
-- longer hold provider keys, so every existing provider starts without one.
ALTER TABLE providers ADD COLUMN type TEXT NOT NULL DEFAULT 'anthropic';
ALTER TABLE providers ADD COLUMN auth_header TEXT NOT NULL DEFAULT '';
ALTER TABLE providers ADD COLUMN credential_sealed TEXT NOT NULL DEFAULT '';
ALTER TABLE providers ADD COLUMN credential_ref TEXT NOT NULL DEFAULT '';

-- A Claude subscription is no longer a provider. A panel that used one is
-- removed with it, and the owner sets a new one.
DELETE FROM eval_panel e WHERE EXISTS (
    SELECT 1 FROM providers p WHERE p.adapter = 'subscription'
      AND (p.id = e.judge_provider OR e.members @> jsonb_build_array(jsonb_build_object('provider_id', p.id::text))));
DELETE FROM providers WHERE adapter = 'subscription';

-- An Anthropic-format gateway on OpenRouter becomes the openrouter type on its
-- default endpoint. Any other gateway keeps its URL as an Anthropic endpoint.
UPDATE providers SET type = 'openrouter', base_url = '' WHERE adapter = 'gateway' AND base_url LIKE '%openrouter.ai%';
UPDATE providers SET type = 'anthropic' WHERE adapter = 'gateway' AND type <> 'openrouter';

ALTER TABLE providers DROP COLUMN adapter;
ALTER TABLE providers DROP COLUMN auth_style;
ALTER TABLE providers DROP COLUMN credential_env;
ALTER TABLE providers ADD CONSTRAINT providers_type_check
    CHECK (type IN ('anthropic', 'openai', 'openrouter', 'openai-compatible'));

-- A worker reports the harness its sandbox image runs. It no longer reports
-- credential names or pins its own provider.
ALTER TABLE workers ADD COLUMN harness TEXT NOT NULL DEFAULT '';

-- One progress record a worker posts for an eval job.
CREATE TABLE eval_job_events (
    id          BIGSERIAL PRIMARY KEY,
    job_id      UUID NOT NULL REFERENCES eval_jobs(id) ON DELETE CASCADE,
    at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    kind        TEXT NOT NULL,
    data        JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX eval_job_events_job ON eval_job_events (job_id, id);

-- The last good copy of the models.dev catalog, trimmed to what the picker
-- shows.
CREATE TABLE model_catalog (
    id          INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    etag        TEXT NOT NULL DEFAULT '',
    body        JSONB NOT NULL,
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
ALTER TABLE workers DROP COLUMN harness;
DROP TABLE model_catalog;
DROP TABLE eval_job_events;
ALTER TABLE providers DROP CONSTRAINT providers_type_check;
ALTER TABLE providers ADD COLUMN adapter TEXT NOT NULL DEFAULT 'anthropic';
ALTER TABLE providers ADD COLUMN auth_style TEXT NOT NULL DEFAULT 'x-api-key';
ALTER TABLE providers ADD COLUMN credential_env TEXT NOT NULL DEFAULT 'ANTHROPIC_API_KEY';
UPDATE providers SET adapter = 'gateway', auth_style = 'bearer', credential_env = 'OPENROUTER_API_KEY', base_url = 'https://openrouter.ai/api' WHERE type = 'openrouter';
ALTER TABLE providers DROP COLUMN credential_ref;
ALTER TABLE providers DROP COLUMN credential_sealed;
ALTER TABLE providers DROP COLUMN auth_header;
ALTER TABLE providers DROP COLUMN type;

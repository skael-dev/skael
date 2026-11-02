-- +goose Up
-- auth-all owns people from here on. Renaming keeps user ids, and every foreign
-- key follows the rename. Passwords do not move: auth-all verifies only argon2id.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM users GROUP BY lower(trim(email)) HAVING count(*) > 1) THEN
        RAISE EXCEPTION 'two users differ only by email case or spacing: merge or rename them, then restart';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE users RENAME TO auth_users;
ALTER TABLE auth_users RENAME COLUMN name TO display_name;
ALTER TABLE auth_users DROP COLUMN password_hash;
ALTER TABLE auth_users DROP COLUMN password_reset_required;
ALTER TABLE auth_users DROP CONSTRAINT users_email_key;
ALTER TABLE auth_users ADD COLUMN email_normalized TEXT;
UPDATE auth_users SET email_normalized = lower(trim(email));
ALTER TABLE auth_users ALTER COLUMN email_normalized SET NOT NULL;
ALTER TABLE auth_users
    ADD COLUMN email_verified_at TIMESTAMPTZ,
    ADD COLUMN image_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN disabled_at TIMESTAMPTZ,
    ADD COLUMN must_change_password BOOLEAN NOT NULL DEFAULT false;
CREATE UNIQUE INDEX auth_users_email_normalized_key ON auth_users (email_normalized);
CREATE INDEX auth_users_role_idx ON auth_users (role);

DROP TABLE sessions;

CREATE TABLE auth_oauth_states (
    id UUID NOT NULL PRIMARY KEY,
    state_hash TEXT NOT NULL,
    provider TEXT NOT NULL,
    verifier TEXT NOT NULL,
    nonce TEXT NOT NULL,
    redirect_to TEXT NOT NULL,
    link_user_id UUID,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX auth_oauth_states_state_hash_key ON auth_oauth_states (state_hash);

CREATE TABLE auth_accounts (
    id UUID NOT NULL PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    provider_account_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX auth_accounts_provider_key ON auth_accounts (provider, provider_account_id);
CREATE UNIQUE INDEX auth_accounts_user_provider_key ON auth_accounts (user_id, provider);

CREATE TABLE auth_credentials (
    user_id UUID NOT NULL PRIMARY KEY REFERENCES auth_users(id) ON DELETE CASCADE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE auth_sessions (
    id UUID NOT NULL PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX auth_sessions_token_hash_key ON auth_sessions (token_hash);
CREATE INDEX auth_sessions_user_id_idx ON auth_sessions (user_id);

CREATE TABLE auth_tokens (
    id UUID NOT NULL PRIMARY KEY,
    user_id UUID REFERENCES auth_users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    identifier TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX auth_tokens_kind_hash_key ON auth_tokens (kind, token_hash);
CREATE INDEX auth_tokens_kind_identifier_idx ON auth_tokens (kind, identifier);

CREATE TABLE auth_totp (
    user_id UUID NOT NULL PRIMARY KEY REFERENCES auth_users(id) ON DELETE CASCADE,
    secret TEXT NOT NULL,
    confirmed_at TIMESTAMPTZ,
    last_step BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE auth_totp_recovery (
    id UUID NOT NULL PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX auth_totp_recovery_code_hash_key ON auth_totp_recovery (code_hash);
CREATE INDEX auth_totp_recovery_user_idx ON auth_totp_recovery (user_id);

CREATE TABLE auth_rate_limits (
    key TEXT NOT NULL PRIMARY KEY,
    window_start TIMESTAMPTZ NOT NULL,
    count BIGINT NOT NULL
);

CREATE TABLE auth_bootstrap (
    id TEXT NOT NULL PRIMARY KEY,
    user_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE auth_bootstrap;
DROP TABLE auth_rate_limits;
DROP TABLE auth_totp_recovery;
DROP TABLE auth_totp;
DROP TABLE auth_tokens;
DROP TABLE auth_sessions;
DROP TABLE auth_credentials;
DROP TABLE auth_accounts;
DROP TABLE auth_oauth_states;

CREATE TABLE sessions (
    token  TEXT PRIMARY KEY,
    data   BYTEA NOT NULL,
    expiry TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_sessions_expiry ON sessions (expiry);

DROP INDEX auth_users_role_idx;
DROP INDEX auth_users_email_normalized_key;
ALTER TABLE auth_users
    DROP COLUMN must_change_password,
    DROP COLUMN disabled_at,
    DROP COLUMN updated_at,
    DROP COLUMN image_url,
    DROP COLUMN email_verified_at,
    DROP COLUMN email_normalized,
    ADD COLUMN password_reset_required BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN password_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE auth_users ADD CONSTRAINT users_email_key UNIQUE (email);
ALTER TABLE auth_users RENAME COLUMN display_name TO name;
ALTER TABLE auth_users RENAME TO users;

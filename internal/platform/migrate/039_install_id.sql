-- +goose Up
-- A random id for the anonymous install ping. It names no person or company.
ALTER TABLE onboarding ADD COLUMN install_id UUID NOT NULL DEFAULT gen_random_uuid();

-- +goose Down
ALTER TABLE onboarding DROP COLUMN install_id;

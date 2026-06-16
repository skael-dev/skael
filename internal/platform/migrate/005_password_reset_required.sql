-- +goose Up
ALTER TABLE users ADD COLUMN password_reset_required BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users DROP COLUMN password_reset_required;

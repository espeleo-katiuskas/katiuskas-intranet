-- +goose Up
-- Having an accounts row at all is already the access switch (see
-- users.hasAccessSelect); is_active was a second, redundant one that
-- the app never actually exposed a way to flip after account creation.
ALTER TABLE accounts DROP COLUMN is_active;

-- +goose Down
ALTER TABLE accounts ADD COLUMN is_active boolean NOT NULL DEFAULT true;

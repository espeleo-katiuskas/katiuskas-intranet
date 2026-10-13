-- +goose Up
-- A license doesn't have to have been paid through the club: someone may
-- have federated elsewhere, and that is the only way to record it. The
-- foreign key stays, so a license that does name a transaction still has
-- to name a real one.
ALTER TABLE licenses ALTER COLUMN transaction_id DROP NOT NULL;

-- +goose Down
-- Fails if any license has no transaction: there is nothing sensible to
-- invent for it.
ALTER TABLE licenses ALTER COLUMN transaction_id SET NOT NULL;

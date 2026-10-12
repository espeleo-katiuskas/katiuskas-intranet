-- +goose Up
-- An account that's no longer used (closed, obsolete) can't be deleted:
-- its entries are history. Flagging it hidden lets the frontend leave it
-- out of the chart and out of the "otra cuenta" pickers unless asked,
-- while everything recorded against it stays exactly as it was.
ALTER TABLE accounts ADD COLUMN hidden boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE accounts DROP COLUMN hidden;

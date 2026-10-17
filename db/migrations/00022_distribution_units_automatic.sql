-- +goose Up
-- A distribution for all members doesn't need anybody to sign up: every member
-- on its cutoff date gets their own unit automatically (see
-- distributions.Repository.SyncAutomaticUnits). Nobody entered that unit, so
-- registered_by_id is NULL until somebody chooses what it should be — when it
-- takes the person who did, with the moment they did it — and a unit with
-- options that nobody has chosen yet is just one whose choices are empty.
ALTER TABLE distribution_units ALTER COLUMN registered_by_id DROP NOT NULL;

-- +goose Down
-- Fails while any automatic unit is still unattributed: there is nobody
-- sensible to blame it on.
ALTER TABLE distribution_units ALTER COLUMN registered_by_id SET NOT NULL;

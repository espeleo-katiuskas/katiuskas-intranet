-- +goose Up
-- An activity is a single day, not a date range, and doesn't need to
-- pin down a place, a difficulty rating, or a headcount cap up front —
-- description covers all of that in free text now.
ALTER TABLE activities DROP CONSTRAINT end_not_before_start;
ALTER TABLE activities DROP COLUMN end_date;
ALTER TABLE activities DROP COLUMN location;
ALTER TABLE activities DROP COLUMN max_participants;
ALTER TABLE activities DROP COLUMN difficulty;
ALTER TABLE activities RENAME COLUMN start_date TO date;
ALTER INDEX idx_activities_start_date RENAME TO idx_activities_date;

-- +goose Down
ALTER INDEX idx_activities_date RENAME TO idx_activities_start_date;
ALTER TABLE activities RENAME COLUMN date TO start_date;
ALTER TABLE activities ADD COLUMN difficulty smallint;
ALTER TABLE activities ADD COLUMN max_participants integer;
ALTER TABLE activities ADD COLUMN location text NOT NULL DEFAULT '';
ALTER TABLE activities ALTER COLUMN location DROP DEFAULT;
ALTER TABLE activities ADD COLUMN end_date date;
ALTER TABLE activities ADD CONSTRAINT end_not_before_start
    CHECK (end_date IS NULL OR end_date >= start_date);

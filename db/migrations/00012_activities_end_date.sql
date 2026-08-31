-- +goose Up
-- date is renamed back to start_date now that an activity can span more
-- than one day again: end_date is mandatory and must not precede it.
-- Existing rows get end_date = start_date (still valid: a single-day
-- activity's start and end date are the same day).
ALTER TABLE activities RENAME COLUMN date TO start_date;
ALTER INDEX idx_activities_date RENAME TO idx_activities_start_date;
ALTER TABLE activities ADD COLUMN end_date date;
UPDATE activities SET end_date = start_date;
ALTER TABLE activities ALTER COLUMN end_date SET NOT NULL;
ALTER TABLE activities ADD CONSTRAINT end_not_before_start
    CHECK (end_date >= start_date);

-- +goose Down
ALTER TABLE activities DROP CONSTRAINT end_not_before_start;
ALTER TABLE activities DROP COLUMN end_date;
ALTER INDEX idx_activities_start_date RENAME TO idx_activities_date;
ALTER TABLE activities RENAME COLUMN start_date TO date;

-- +goose Up
-- A temporary leave ("baja temporal") can now be open-ended, the same
-- way a membership period itself already could: end_date is only known
-- once the person actually comes back, so a leave that's still ongoing
-- records no end_date at all yet rather than requiring a guessed one
-- up front. The generated period column already handles a NULL upper
-- bound correctly (an unbounded daterange), so nothing else about it,
-- or the overlap-exclusion constraint built on it, needs to change.
ALTER TABLE membership_leaves ALTER COLUMN end_date DROP NOT NULL;
ALTER TABLE membership_leaves DROP CONSTRAINT leave_end_not_before_start;
ALTER TABLE membership_leaves ADD CONSTRAINT leave_end_not_before_start
    CHECK (end_date IS NULL OR end_date >= start_date);

-- +goose Down
ALTER TABLE membership_leaves DROP CONSTRAINT leave_end_not_before_start;
ALTER TABLE membership_leaves ADD CONSTRAINT leave_end_not_before_start
    CHECK (end_date >= start_date);
ALTER TABLE membership_leaves ALTER COLUMN end_date SET NOT NULL;

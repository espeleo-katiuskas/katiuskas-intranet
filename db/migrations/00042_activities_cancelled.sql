-- +goose Up
-- An activity's status was a column somebody had to keep right by hand: nothing ever moved one from
-- planned to completed when its date came. The only thing that is not a consequence of the dates is
-- that it was cancelled, so that is all that is stored; whether it is still to come, going on or over
-- is worked out from start_date and end_date (and today's date) wherever it is shown.
ALTER TABLE activities ADD COLUMN cancelled boolean NOT NULL DEFAULT false;
UPDATE activities SET cancelled = (status = 'cancelled');
ALTER TABLE activities DROP COLUMN status;
DROP TYPE activity_status;

-- +goose Down
CREATE TYPE activity_status AS ENUM ('planned', 'completed', 'cancelled');
ALTER TABLE activities ADD COLUMN status activity_status NOT NULL DEFAULT 'planned';
UPDATE activities SET status = CASE
    WHEN cancelled THEN 'cancelled'::activity_status
    WHEN end_date < CURRENT_DATE THEN 'completed'::activity_status
    ELSE 'planned'::activity_status
END;
ALTER TABLE activities DROP COLUMN cancelled;

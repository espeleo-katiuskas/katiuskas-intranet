-- +goose Up
-- How many requests the API has answered, per minute (only the minutes in which there was any), for
-- the activity figures of /admin. The API counts in memory and adds to this table every few seconds,
-- so the numbers survive a restart.
CREATE TABLE request_counts (
    minute timestamptz PRIMARY KEY,
    requests bigint NOT NULL CHECK (requests > 0)
);

-- +goose Down
DROP TABLE request_counts;

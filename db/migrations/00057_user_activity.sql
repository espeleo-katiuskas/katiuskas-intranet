-- +goose Up
-- Who used the API each day, and how much: one row per person and day (the day of the club's own time
-- zone, Europe/Madrid) with the number of requests they made. It feeds the calendar of activity of
-- /admin. The API counts in memory and adds to this table together with request_counts, and a request
-- of an administrator acting as somebody else counts for nobody.
CREATE TABLE user_activity (
    day date NOT NULL,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    requests bigint NOT NULL CHECK (requests > 0),
    PRIMARY KEY (day, user_id)
);

CREATE INDEX idx_user_activity_user_id ON user_activity(user_id);

-- +goose Down
DROP TABLE user_activity;

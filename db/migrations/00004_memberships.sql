-- +goose Up
CREATE TABLE memberships (
    id                 bigserial PRIMARY KEY,
    user_id            bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    membership_number  text,
    start_date         date NOT NULL,
    end_date           date,
    end_reason         text,
    period             daterange GENERATED ALWAYS AS
                          (daterange(start_date, end_date, '[]')) STORED,
    CONSTRAINT end_not_before_start
        CHECK (end_date IS NULL OR end_date >= start_date),
    -- prevents two membership periods of the same user from overlapping
    EXCLUDE USING gist (user_id WITH =, period WITH &&)
);

CREATE INDEX idx_memberships_user_id ON memberships(user_id);

-- +goose Down
DROP TABLE memberships;

-- +goose Up
CREATE TYPE board_position AS ENUM (
    'president', 'vice_president', 'secretary', 'treasurer', 'member_at_large'
);

-- Who's served on the Junta Directiva, and when: "board" is computed
-- live from whoever currently has an open period here (see
-- roles.Repository.ListNamesForUser), the same way member/
-- former_member are computed from memberships — never granted or
-- revoked by hand.
CREATE TABLE board_members (
    id         bigserial PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    position   board_position NOT NULL,
    start_date date NOT NULL,
    end_date   date,
    period     daterange GENERATED ALWAYS AS
                  (daterange(start_date, end_date, '[]')) STORED,
    CONSTRAINT board_member_end_not_before_start
        CHECK (end_date IS NULL OR end_date >= start_date),
    -- prevents someone from holding two board terms — whatever the
    -- position — at the same time
    EXCLUDE USING gist (user_id WITH =, period WITH &&)
);

CREATE INDEX idx_board_members_user_id ON board_members(user_id);

-- +goose Down
DROP TABLE board_members;
DROP TYPE board_position;

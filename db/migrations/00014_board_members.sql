-- +goose Up
CREATE TYPE board_position AS ENUM (
    'president', 'vice_president', 'secretary', 'treasurer', 'member_at_large'
);

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

-- "board" stops being a role admins grant or revoke by hand — it's
-- computed live from whoever currently has an open board_members period,
-- the same way member/former_member are computed from memberships (see
-- roles.Repository.ListNamesForUser).
UPDATE roles SET is_automatic = true WHERE name = 'board';

-- Anyone who already held "board" the old way becomes a board_members
-- row instead, so the automatic role keeps working for them without a
-- gap. Their real start date and position aren't recorded anywhere, so
-- this uses today's date and a placeholder position (member_at_large /
-- vocal) — worth correcting by hand on /member once this lands.
INSERT INTO board_members (user_id, position, start_date)
SELECT ur.user_id, 'member_at_large', CURRENT_DATE
FROM user_roles ur
JOIN roles r ON r.id = ur.role_id
WHERE r.name = 'board';

DELETE FROM user_roles WHERE role_id = (SELECT id FROM roles WHERE name = 'board');

-- +goose Down
INSERT INTO user_roles (user_id, role_id)
SELECT bm.user_id, (SELECT id FROM roles WHERE name = 'board')
FROM board_members bm
WHERE bm.period @> CURRENT_DATE
ON CONFLICT DO NOTHING;

UPDATE roles SET is_automatic = false WHERE name = 'board';

DROP TABLE board_members;
DROP TYPE board_position;

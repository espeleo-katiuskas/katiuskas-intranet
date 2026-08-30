-- +goose Up
-- A temporary leave ("baja temporal"): a bounded window within a
-- membership period where the person is stepping away for a while,
-- without actually leaving the club (that's memberships.end_date).
-- Containment within the parent period isn't checked here — this
-- codebase has no triggers, and cross-table containment isn't something
-- a CHECK constraint can express — the memberships package validates it
-- before every insert/update instead.
CREATE TABLE membership_leaves (
    id             bigserial PRIMARY KEY,
    membership_id  bigint NOT NULL REFERENCES memberships(id) ON DELETE CASCADE,
    start_date     date NOT NULL,
    end_date       date NOT NULL,
    reason         text,
    period         daterange GENERATED ALWAYS AS
                      (daterange(start_date, end_date, '[]')) STORED,
    CONSTRAINT leave_end_not_before_start
        CHECK (end_date >= start_date),
    -- prevents two temporary leaves in the same membership period from overlapping
    EXCLUDE USING gist (membership_id WITH =, period WITH &&)
);

CREATE INDEX idx_membership_leaves_membership_id ON membership_leaves(membership_id);

-- +goose Down
DROP TABLE membership_leaves;

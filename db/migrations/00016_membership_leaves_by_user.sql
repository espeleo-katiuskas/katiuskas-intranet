-- +goose Up
-- A temporary leave belongs to the person, not to one specific
-- membership period: user_id replaces membership_id. Containment
-- within a membership period is still enforced (memberships.Service
-- looks up, by date range, which single period — if any — of that
-- user's own history fully contains a leave, the same invariant as
-- before, just no longer expressed as a stored foreign key to one
-- particular period). This also means every one of a person's
-- temporary leaves, across however many separate alta/baja spans
-- they've had, is now a single user_id lookup — no join through
-- memberships needed to answer "is this person currently on leave" or
-- "how many leaves has this person had".
ALTER TABLE membership_leaves ADD COLUMN user_id bigint REFERENCES users(id) ON DELETE CASCADE;
UPDATE membership_leaves ml SET user_id = m.user_id FROM memberships m WHERE m.id = ml.membership_id;
ALTER TABLE membership_leaves ALTER COLUMN user_id SET NOT NULL;

ALTER TABLE membership_leaves DROP CONSTRAINT membership_leaves_membership_id_period_excl;
ALTER TABLE membership_leaves ADD CONSTRAINT membership_leaves_user_id_period_excl
    EXCLUDE USING gist (user_id WITH =, period WITH &&);

DROP INDEX idx_membership_leaves_membership_id;
CREATE INDEX idx_membership_leaves_user_id ON membership_leaves(user_id);

ALTER TABLE membership_leaves DROP COLUMN membership_id;

-- +goose Down
ALTER TABLE membership_leaves ADD COLUMN membership_id bigint REFERENCES memberships(id) ON DELETE CASCADE;
UPDATE membership_leaves ml SET membership_id = (
    SELECT m.id FROM memberships m WHERE m.user_id = ml.user_id AND m.period @> ml.period
);
ALTER TABLE membership_leaves ALTER COLUMN membership_id SET NOT NULL;

ALTER TABLE membership_leaves DROP CONSTRAINT membership_leaves_user_id_period_excl;
ALTER TABLE membership_leaves ADD CONSTRAINT membership_leaves_membership_id_period_excl
    EXCLUDE USING gist (membership_id WITH =, period WITH &&);

DROP INDEX idx_membership_leaves_user_id;
CREATE INDEX idx_membership_leaves_membership_id ON membership_leaves(membership_id);

ALTER TABLE membership_leaves DROP COLUMN user_id;

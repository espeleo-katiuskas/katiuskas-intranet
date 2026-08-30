-- +goose Up
-- A membership number identifies a person for as long as they're
-- associated with the club, not any single stretch of it — someone who
-- leaves and later rejoins keeps the same one. Stored per membership
-- period, there was nothing stopping a rejoin from getting a new number
-- instead of keeping their original. Move it onto the person; a period
-- is just a date range now.
ALTER TABLE users ADD COLUMN membership_number text;

-- Backfill from each person's earliest period that had a number — the
-- one assigned when they first joined, not a later one that might have
-- drifted from it.
UPDATE users u SET membership_number = earliest.membership_number
FROM (
    SELECT DISTINCT ON (user_id) user_id, membership_number
    FROM memberships
    WHERE membership_number IS NOT NULL
    ORDER BY user_id, start_date ASC
) earliest
WHERE earliest.user_id = u.id;

CREATE UNIQUE INDEX idx_users_membership_number ON users(membership_number) WHERE membership_number IS NOT NULL;

ALTER TABLE memberships DROP COLUMN membership_number;

-- +goose Down
-- Lossy: there's no record of which period(s) actually had the number
-- before it moved to the person, so every period the person has gets it.
ALTER TABLE memberships ADD COLUMN membership_number text;

UPDATE memberships m SET membership_number = u.membership_number
FROM users u
WHERE u.id = m.user_id AND u.membership_number IS NOT NULL;

DROP INDEX idx_users_membership_number;
ALTER TABLE users DROP COLUMN membership_number;

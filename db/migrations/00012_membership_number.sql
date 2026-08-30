-- +goose Up
-- membership_number is no longer free text an admin types in: it's a
-- non-negative integer assigned automatically the first time someone
-- gets an alta (see memberships.Service.Create and
-- users.Repository.EnsureMembershipNumber), never touched again by a
-- later profile edit or a later rejoin.
ALTER TABLE users
    ALTER COLUMN membership_number TYPE integer USING NULLIF(membership_number, '')::integer;
ALTER TABLE users ADD CONSTRAINT membership_number_non_negative CHECK (membership_number >= 0);

-- Backfill: give every person who has ever had an alta but doesn't yet
-- have a number the next one after today's highest, in the order they
-- first joined — as if automatic assignment had been there from the
-- start.
WITH highest AS (
    SELECT COALESCE(MAX(membership_number), -1) AS current_max FROM users
),
first_altas AS (
    SELECT user_id, MIN(start_date) AS first_alta FROM memberships GROUP BY user_id
),
missing AS (
    SELECT u.id, f.first_alta
    FROM users u
    JOIN first_altas f ON f.user_id = u.id
    WHERE u.membership_number IS NULL
),
numbered AS (
    SELECT missing.id, highest.current_max + ROW_NUMBER() OVER (ORDER BY missing.first_alta, missing.id) AS new_number
    FROM missing, highest
)
UPDATE users SET membership_number = numbered.new_number
FROM numbered
WHERE users.id = numbered.id;

-- +goose Down
-- Lossy in spirit (the auto-assignment behavior itself doesn't come
-- back), but the data survives as text.
ALTER TABLE users DROP CONSTRAINT membership_number_non_negative;
ALTER TABLE users ALTER COLUMN membership_number TYPE text USING membership_number::text;

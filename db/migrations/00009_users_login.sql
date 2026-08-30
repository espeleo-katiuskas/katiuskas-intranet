-- +goose Up
CREATE EXTENSION IF NOT EXISTS unaccent;

ALTER TABLE users ADD COLUMN login text;

-- Backfill every existing person with the same shape a new one gets from
-- users.Repository.generateLogin: first_name.last_name, accents folded
-- to plain ASCII via unaccent(), anything else collapsed to a single
-- hyphen, de-duplicated with a numeric suffix (2, 3, ...) per repeated
-- base so two people who'd otherwise land on the same slug still end up
-- unique.
WITH base AS (
    SELECT id,
        trim(both '-' from regexp_replace(lower(unaccent(first_name)), '[^a-z0-9]+', '-', 'g'))
        || '.' ||
        trim(both '-' from regexp_replace(lower(unaccent(last_name)), '[^a-z0-9]+', '-', 'g'))
        AS slug
    FROM users
),
numbered AS (
    SELECT id, slug, row_number() OVER (PARTITION BY slug ORDER BY id) AS rn
    FROM base
)
UPDATE users u
SET login = numbered.slug || CASE WHEN numbered.rn = 1 THEN '' ELSE numbered.rn::text END
FROM numbered
WHERE u.id = numbered.id;

ALTER TABLE users ALTER COLUMN login SET NOT NULL;
ALTER TABLE users ADD CONSTRAINT users_login_key UNIQUE (login);

-- +goose Down
ALTER TABLE users DROP CONSTRAINT users_login_key;
ALTER TABLE users DROP COLUMN login;
DROP EXTENSION IF EXISTS unaccent;

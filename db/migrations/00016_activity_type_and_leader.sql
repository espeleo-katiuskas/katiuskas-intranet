-- +goose Up
-- "outdoor" never fit well as its own category and nothing used it in
-- practice; "canyoning" ("cañones") is a real, distinct kind of outing
-- this club runs. Recreating the enum (Postgres can't just rename a
-- value to something semantically different) is safe as long as no row
-- currently uses 'outdoor' — the USING cast below fails loudly if one
-- ever does, rather than silently coercing it into something wrong.
ALTER TYPE activity_type RENAME TO activity_type_old;
CREATE TYPE activity_type AS ENUM ('caving', 'canyoning', 'training', 'social', 'other');
ALTER TABLE activities ALTER COLUMN type DROP DEFAULT;
ALTER TABLE activities ALTER COLUMN type TYPE activity_type USING type::text::activity_type;
ALTER TABLE activities ALTER COLUMN type SET DEFAULT 'caving';
DROP TYPE activity_type_old;

-- Every activity now needs an organizer (see activities.Service.Create,
-- which sets it to whoever creates it — never left to the caller).
-- Existing activities predating that rule get the first admin as a
-- placeholder; there's always at least one (see roles.ErrLastAdmin).
UPDATE activities SET leader_id = (
    SELECT u.id FROM users u
    JOIN user_roles ur ON ur.user_id = u.id
    JOIN roles r ON r.id = ur.role_id
    WHERE r.name = 'admin'
    ORDER BY u.id LIMIT 1
) WHERE leader_id IS NULL;
ALTER TABLE activities ALTER COLUMN leader_id SET NOT NULL;

-- +goose Down
ALTER TABLE activities ALTER COLUMN leader_id DROP NOT NULL;

ALTER TYPE activity_type RENAME TO activity_type_new;
CREATE TYPE activity_type AS ENUM ('caving', 'training', 'outdoor', 'social', 'other');
ALTER TABLE activities ALTER COLUMN type DROP DEFAULT;
ALTER TABLE activities ALTER COLUMN type TYPE activity_type USING type::text::activity_type;
ALTER TABLE activities ALTER COLUMN type SET DEFAULT 'caving';
DROP TYPE activity_type_new;

-- +goose Up
-- Public (the only kind that existed before this column) works exactly
-- as before: anyone can join. Private restricts who can add a
-- participant at all — organizer, board, or admin only — enforced in
-- activities.Handler.join, not here; a plain column value with no
-- special meaning to Postgres.
CREATE TYPE activity_visibility AS ENUM ('public', 'private');
ALTER TABLE activities ADD COLUMN visibility activity_visibility NOT NULL DEFAULT 'public';

-- +goose Down
ALTER TABLE activities DROP COLUMN visibility;
DROP TYPE activity_visibility;

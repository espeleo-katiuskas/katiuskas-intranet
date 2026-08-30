-- +goose Up
CREATE TYPE gender AS ENUM ('male', 'female');

-- Nullable: not everyone already on file has this recorded, and it's
-- never required to sign up or sign in.
ALTER TABLE users ADD COLUMN gender gender;

-- +goose Down
ALTER TABLE users DROP COLUMN gender;
DROP TYPE gender;

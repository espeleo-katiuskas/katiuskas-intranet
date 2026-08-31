-- +goose Up
-- birth_date is now required — age_category (and, before it, the club's
-- own paperwork) always needs it, so a person can no longer be created
-- without one. email, conversely, is no longer required: someone with no
-- email on file simply can never receive a magic link (see
-- users.Repository.FindActiveByEmailAndPhone), the same way someone with
-- no phone already couldn't.
ALTER TABLE users ALTER COLUMN birth_date SET NOT NULL;
ALTER TABLE users ALTER COLUMN email DROP NOT NULL;

-- +goose Down
ALTER TABLE users ALTER COLUMN email SET NOT NULL;
ALTER TABLE users ALTER COLUMN birth_date DROP NOT NULL;

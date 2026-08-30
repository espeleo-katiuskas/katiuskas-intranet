-- +goose Up
-- email and phone can now each hold more than one value, space-separated
-- (like emergency_contact already allows for multiple contacts) rather
-- than a child table: matching "does any of these belong to this
-- person" is a straightforward array-overlap check (see
-- users.Repository), and this club's scale doesn't need anything
-- heavier. The UNIQUE constraint on email can't survive that as a plain
-- column constraint — one email still can't belong to two people, but
-- Postgres can't express "no two rows share an element" that way, so
-- it's enforced in Go instead (users.Repository.emailTakenByAnotherUser).
ALTER TABLE users DROP CONSTRAINT users_email_key;

-- +goose Down
-- Lossy if, by then, any single row's email holds more than one address.
ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);

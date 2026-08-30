-- +goose Up
CREATE TYPE gender AS ENUM ('male', 'female');

-- There's no password: signing in happens through a one-time magic
-- link instead (magic_link_tokens below). Anyone in this table can
-- request one as long as they have an email and phone on file; what a
-- resulting session can actually do is entirely down to roles (see
-- 00003_roles.sql), not anything stored here. email and phone can each
-- hold more than one value, space-separated (like emergency_contact
-- already allows for multiple contacts) — matching "does any of these
-- belong to this person" is a Go-side array-overlap check (see
-- users.Repository); one email still can't belong to two people, but
-- Postgres can't express "no two rows share an element" as a plain
-- column constraint, so that's enforced in Go too
-- (users.Repository.emailTakenByAnotherUser).
CREATE TABLE users (
    id                 bigserial PRIMARY KEY,
    first_name         text NOT NULL,
    last_name          text NOT NULL,
    national_id        text UNIQUE,
    birth_date         date,
    gender             gender,
    email              text NOT NULL,
    phone              text,
    address            text,
    postal_code        text,
    city               text,
    province           text,
    emergency_contact  text,
    -- Identifies a person for as long as they're associated with the
    -- club, not any single membership period — someone who leaves and
    -- later rejoins keeps the same number. Assigned automatically the
    -- first time someone gets an alta (see memberships.Service.Create
    -- and users.Repository.EnsureMembershipNumber), never chosen by
    -- hand and never touched again by a later rejoin.
    membership_number  integer CONSTRAINT membership_number_non_negative CHECK (membership_number >= 0),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    last_login_at      timestamptz,
    last_login_ip      text
);

CREATE UNIQUE INDEX idx_users_membership_number ON users(membership_number) WHERE membership_number IS NOT NULL;

CREATE TABLE tokens (
    id               bigserial PRIMARY KEY,
    user_id          bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash       text NOT NULL UNIQUE,
    expires_at       timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    revoked_at       timestamptz,
    last_used_at     timestamptz,
    last_used_ip     text,
    -- Set only on a token minted by POST /users/{userID}/impersonate:
    -- which admin is behind this session. Everything else about the
    -- token — user_id, roles resolution, expiry — is exactly like a
    -- normal one, so the impersonated session naturally can only do
    -- what that person could (see tokens.Service.Impersonate).
    impersonated_by  bigint REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX idx_tokens_user_id ON tokens(user_id);

-- A magic-link email carries both a one-time link and a 6-digit numeric
-- code, for signing in from a device other than the one the email
-- arrived on. Both represent the same sign-in attempt, so their hashes
-- live in the same row and consuming either one consumes both.
-- code_attempts bounds brute-forcing the low-entropy code: it's bumped
-- on every wrong guess and stops accepting the code (never the link)
-- past a threshold — see users.Repository.ConsumeMagicLinkCode.
CREATE TABLE magic_link_tokens (
    id             bigserial PRIMARY KEY,
    user_id        bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash     text NOT NULL UNIQUE,
    code_hash      text,
    code_attempts  integer NOT NULL DEFAULT 0,
    expires_at     timestamptz NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    consumed_at    timestamptz
);

CREATE INDEX idx_magic_link_tokens_user_id ON magic_link_tokens(user_id);

-- +goose Down
DROP TABLE magic_link_tokens;
DROP TABLE tokens;
DROP TABLE users;
DROP TYPE gender;

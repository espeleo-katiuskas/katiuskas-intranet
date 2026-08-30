-- +goose Up
CREATE TABLE users (
    id                 bigserial PRIMARY KEY,
    first_name         text NOT NULL,
    last_name          text NOT NULL,
    national_id        text UNIQUE,
    birth_date         date,
    email              text UNIQUE NOT NULL,
    phone              text,
    address            text,
    postal_code        text,
    city               text,
    province           text,
    emergency_contact  text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    last_login_at      timestamptz,
    last_login_ip      text
);

-- There's no password: signing in happens through a one-time magic
-- link instead (magic_link_tokens below). Anyone in this table can
-- request one as long as they have an email and phone on file; what a
-- resulting session can actually do is entirely down to roles (see
-- 00003_roles.sql), not anything stored here.
CREATE TABLE tokens (
    id            bigserial PRIMARY KEY,
    user_id       bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash    text NOT NULL UNIQUE,
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    revoked_at    timestamptz,
    last_used_at  timestamptz,
    last_used_ip  text
);

CREATE INDEX idx_tokens_user_id ON tokens(user_id);

CREATE TABLE magic_link_tokens (
    id          bigserial PRIMARY KEY,
    user_id     bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  text NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    consumed_at timestamptz
);

CREATE INDEX idx_magic_link_tokens_user_id ON magic_link_tokens(user_id);

-- +goose Down
DROP TABLE magic_link_tokens;
DROP TABLE tokens;
DROP TABLE users;

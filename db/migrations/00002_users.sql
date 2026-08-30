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
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- A person can exist without ever having access (no row here) — not
-- everyone registered needs to be able to sign in. There's no password:
-- signing in happens through a one-time magic link instead.
CREATE TABLE accounts (
    user_id        bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    is_active      boolean NOT NULL DEFAULT true,
    last_login_at  timestamptz
);

CREATE TABLE refresh_tokens (
    id          bigserial PRIMARY KEY,
    user_id     bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  text NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    revoked_at  timestamptz
);

CREATE INDEX idx_refresh_tokens_user_id ON refresh_tokens(user_id);

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
DROP TABLE refresh_tokens;
DROP TABLE accounts;
DROP TABLE users;

-- +goose Up
CREATE TYPE account_role AS ENUM ('admin', 'board', 'member');

CREATE TABLE users (
    id                          bigserial PRIMARY KEY,
    first_name                  text NOT NULL,
    last_name                   text NOT NULL,
    national_id                 text UNIQUE,
    birth_date                  date,
    email                       text UNIQUE NOT NULL,
    phone                       text,
    address                     text,
    emergency_contact_name      text,
    emergency_contact_phone     text,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE credentials (
    user_id        bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    username       text UNIQUE NOT NULL,
    password_hash  text NOT NULL,
    role           account_role NOT NULL DEFAULT 'member',
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

-- +goose Down
DROP TABLE refresh_tokens;
DROP TABLE credentials;
DROP TABLE users;
DROP TYPE account_role;

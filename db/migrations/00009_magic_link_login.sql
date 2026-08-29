-- +goose Up
ALTER TABLE credentials RENAME TO accounts;
ALTER TABLE accounts DROP COLUMN username;
ALTER TABLE accounts DROP COLUMN password_hash;

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

-- Lossy: there is no password to restore. Existing accounts get a
-- placeholder username and an unusable password hash, just so the old
-- NOT NULL/UNIQUE shape is satisfiable; nobody could log in with these.
ALTER TABLE accounts ADD COLUMN username text;
ALTER TABLE accounts ADD COLUMN password_hash text;
UPDATE accounts SET username = 'user_' || user_id::text, password_hash = '';
ALTER TABLE accounts ALTER COLUMN username SET NOT NULL;
ALTER TABLE accounts ALTER COLUMN password_hash SET NOT NULL;
ALTER TABLE accounts ADD CONSTRAINT accounts_username_key UNIQUE (username);
ALTER TABLE accounts RENAME TO credentials;

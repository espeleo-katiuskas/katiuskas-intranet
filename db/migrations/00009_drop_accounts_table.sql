-- +goose Up
-- Access is entirely role-driven now (see auth.Protect's baseline):
-- being able to request a magic link never needed a separate accounts
-- row to begin with, since it already requires knowing the person's
-- email *and* phone. Fold the one thing accounts still tracked —
-- login history — directly onto users, and add where from.
ALTER TABLE users ADD COLUMN last_login_at timestamptz;
ALTER TABLE users ADD COLUMN last_login_ip text;

UPDATE users u SET last_login_at = a.last_login_at
FROM accounts a
WHERE a.user_id = u.id AND a.last_login_at IS NOT NULL;

DROP TABLE accounts;

-- +goose Down
CREATE TABLE accounts (
    user_id        bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    last_login_at  timestamptz
);

INSERT INTO accounts (user_id, last_login_at)
SELECT id, last_login_at FROM users WHERE last_login_at IS NOT NULL;

ALTER TABLE users DROP COLUMN last_login_ip;
ALTER TABLE users DROP COLUMN last_login_at;

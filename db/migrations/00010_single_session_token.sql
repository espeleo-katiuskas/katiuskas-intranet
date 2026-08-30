-- +goose Up
-- The access/refresh split bought protection against a leaked bearer
-- token by keeping it short-lived, at the cost of every client needing
-- two tokens and a rotation dance — not worth it for a club-scale app
-- with no untrusted browser clients. One opaque, revocable token now
-- does the whole job: what refresh_tokens already was, just renamed
-- and no longer paired with a short-lived JWT. Track last use (when
-- and from where) so an admin can audit a token's activity.
ALTER TABLE refresh_tokens RENAME TO tokens;
ALTER TABLE tokens ADD COLUMN last_used_at timestamptz;
ALTER TABLE tokens ADD COLUMN last_used_ip text;

ALTER INDEX idx_refresh_tokens_user_id RENAME TO idx_tokens_user_id;

-- +goose Down
ALTER INDEX idx_tokens_user_id RENAME TO idx_refresh_tokens_user_id;
ALTER TABLE tokens DROP COLUMN last_used_ip;
ALTER TABLE tokens DROP COLUMN last_used_at;
ALTER TABLE tokens RENAME TO refresh_tokens;

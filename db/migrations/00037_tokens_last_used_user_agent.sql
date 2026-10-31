-- +goose Up
-- The browser a session was last used from, next to last_used_ip (what the admin's list of sessions
-- shows). It is the whole User-Agent header, cut to a sane length; the browser and operating system
-- are worked out from it when the list is read, so a better parser later improves old rows too.
ALTER TABLE tokens ADD COLUMN last_used_user_agent text;

-- +goose Down
ALTER TABLE tokens DROP COLUMN last_used_user_agent;

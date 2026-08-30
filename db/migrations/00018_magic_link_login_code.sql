-- +goose Up
-- A magic-link email now also carries a short numeric code, for signing
-- in from a device other than the one the email arrived on (typing a
-- 6-digit code is a lot more practical there than a long URL). Its hash
-- lives alongside the link's in the same row, since both represent the
-- same sign-in attempt and either one should consume it. code_attempts
-- bounds brute-forcing the low-entropy code: users.Repository bumps it
-- on every wrong guess and stops accepting the code at all past a
-- threshold, without affecting the link itself, which stays checkable
-- by anyone who actually clicks it.
ALTER TABLE magic_link_tokens ADD COLUMN code_hash text;
ALTER TABLE magic_link_tokens ADD COLUMN code_attempts integer NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE magic_link_tokens DROP COLUMN code_hash;
ALTER TABLE magic_link_tokens DROP COLUMN code_attempts;

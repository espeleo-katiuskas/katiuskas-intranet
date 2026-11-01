-- +goose Up
-- An admin can, for a while, see the intranet as somebody with other roles (only admin, only member,
-- member plus money...) without touching anybody's real roles: the session carries the roles to act
-- with and until when. While it is in force the session's roles are those, not the person's own
-- (resolved live as always); after role_override_until, or when the admin puts it back, they are the
-- person's own again. Nothing else about the session changes.
ALTER TABLE tokens
    ADD COLUMN role_override text[] CHECK (role_override IS NULL OR cardinality(role_override) > 0),
    ADD COLUMN role_override_until timestamptz,
    ADD CONSTRAINT tokens_role_override_has_end CHECK ((role_override IS NULL) = (role_override_until IS NULL));

-- +goose Down
ALTER TABLE tokens
    DROP CONSTRAINT tokens_role_override_has_end,
    DROP COLUMN role_override_until,
    DROP COLUMN role_override;

-- +goose Up
-- Set only on a token minted by POST /users/{userID}/impersonate: who
-- (which admin) is behind this session. Everything else about the token
-- — user_id, roles resolution, expiry — is exactly like a normal one, so
-- the impersonated session naturally can only do what that person could
-- (see tokens.Service.Impersonate / roles.Repository.ListNamesForUser).
ALTER TABLE tokens ADD COLUMN impersonated_by bigint REFERENCES users(id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE tokens DROP COLUMN impersonated_by;

-- +goose Up
-- A session expires a while after its LAST use, not a while after it was created: every use moves
-- expires_at to now() plus the token's own lifetime. The lifetime is per token (30 days for an
-- ordinary one, 3 hours for one opened through a local.* address); NULL means a fixed expiry that
-- uses do not move (an impersonation session, which is meant to end on its own shortly whatever
-- happens).
ALTER TABLE tokens ADD COLUMN lifetime interval CHECK (lifetime > interval '0');

-- The sessions that already exist were issued for 30 days: from now on they slide too.
UPDATE tokens SET lifetime = interval '30 days' WHERE impersonated_by IS NULL;

-- +goose Down
ALTER TABLE tokens DROP COLUMN lifetime;

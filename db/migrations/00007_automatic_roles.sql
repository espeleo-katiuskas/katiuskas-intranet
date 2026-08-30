-- +goose Up
ALTER TABLE roles ADD COLUMN is_automatic boolean NOT NULL DEFAULT false;

-- "member" stops being something admin/board grant or revoke by hand:
-- it's derived from whether the person has a currently active
-- membership period (see roles.Repository.ListNamesForUser). Any
-- existing manual grants are removed since they no longer mean anything
-- — the computed status takes over immediately.
UPDATE roles SET is_automatic = true WHERE name = 'member';
DELETE FROM user_roles WHERE role_id = (SELECT id FROM roles WHERE name = 'member');

-- Computed the same way, but for people who used to have an active
-- membership and don't anymore.
INSERT INTO roles (name, description, is_system, is_automatic) VALUES
    ('former_member', 'Ha sido socio, pero no tiene ninguna alta activa ahora mismo.', true, true);

-- +goose Down
-- Lossy: there's no record of who held "member" by hand before it
-- became automatic, so this only restores it for people who happen to
-- have an active membership right now.
INSERT INTO user_roles (user_id, role_id)
SELECT DISTINCT m.user_id, r.id
FROM memberships m, roles r
WHERE r.name = 'member' AND m.period @> CURRENT_DATE
ON CONFLICT DO NOTHING;

DELETE FROM roles WHERE name = 'former_member';
ALTER TABLE roles DROP COLUMN is_automatic;

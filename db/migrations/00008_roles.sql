-- +goose Up
CREATE TABLE roles (
    id          bigserial PRIMARY KEY,
    name        text UNIQUE NOT NULL,
    description text,
    -- system roles (admin/board/member) are the ones the API actually
    -- grants permissions to; they can't be renamed away or deleted.
    is_system   boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_roles (
    user_id  bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id  bigint NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

INSERT INTO roles (name, description, is_system) VALUES
    ('admin', 'Acceso completo: gestiona socios, actividades, material y roles.', true),
    ('board', 'Directiva: gestiona socios, actividades y material.', true),
    ('member', 'Socio: acceso de lectura y a sus propios datos.', true);

INSERT INTO user_roles (user_id, role_id)
SELECT c.user_id, r.id
FROM credentials c
JOIN roles r ON r.name = c.role::text;

ALTER TABLE credentials DROP COLUMN role;
DROP TYPE account_role;

-- +goose Down
CREATE TYPE account_role AS ENUM ('admin', 'board', 'member');
ALTER TABLE credentials ADD COLUMN role account_role;

-- Lossy: a person with several roles collapses to their single
-- highest-priority one (admin > board > member > anything custom).
UPDATE credentials c SET role = (
    SELECT r.name::account_role
    FROM user_roles ur
    JOIN roles r ON r.id = ur.role_id
    WHERE ur.user_id = c.user_id AND r.name IN ('admin', 'board', 'member')
    ORDER BY CASE r.name WHEN 'admin' THEN 0 WHEN 'board' THEN 1 ELSE 2 END
    LIMIT 1
);
UPDATE credentials SET role = 'member' WHERE role IS NULL;
ALTER TABLE credentials ALTER COLUMN role SET NOT NULL;

DROP TABLE user_roles;
DROP TABLE roles;

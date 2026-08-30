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
    ('member', 'Socio: acceso de lectura y a sus propios datos.', true),
    -- Not a system role: a club that doesn't want this distinction can
    -- just delete it and keep using board for material transactions
    -- instead.
    ('material', 'Gestiona préstamos, alquileres y ventas de material.', false);

-- +goose Down
DROP TABLE user_roles;
DROP TABLE roles;

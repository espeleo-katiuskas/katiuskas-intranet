-- +goose Up
CREATE TABLE roles (
    id            bigserial PRIMARY KEY,
    name          text UNIQUE NOT NULL,
    description   text,
    -- system roles (admin/board/member/former_member) are the ones the
    -- API actually grants permissions to or wires automatic behavior
    -- to; they can't be renamed away or deleted.
    is_system     boolean NOT NULL DEFAULT false,
    -- automatic roles (member/former_member) are computed from
    -- membership history rather than granted or revoked by hand — see
    -- roles.Repository.ListNamesForUser.
    is_automatic  boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_roles (
    user_id  bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id  bigint NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

INSERT INTO roles (name, description, is_system, is_automatic) VALUES
    ('admin', 'Acceso completo: gestiona socios, actividades, material y roles.', true, false),
    ('board', 'Directiva: gestiona socios, actividades y material.', true, false),
    ('member', 'Socio: acceso de lectura y a sus propios datos.', true, true),
    ('former_member', 'Ha sido socio, pero no tiene ninguna alta activa ahora mismo.', true, true),
    -- Not a system role: a club that doesn't want this distinction can
    -- just delete it and keep using board for material transactions
    -- instead.
    ('material', 'Gestiona préstamos, alquileres y ventas de material.', false, false);

-- +goose Down
DROP TABLE user_roles;
DROP TABLE roles;

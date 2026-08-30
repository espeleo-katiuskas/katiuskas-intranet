-- +goose Up
-- The catalog of federative license names: every (zone × modality × age
-- bracket) combination plus a handful of standalone names, kept in the
-- database rather than a Go enum so the list can grow (a new zone, a
-- new federation) with an INSERT instead of a code deploy. is_other
-- flags the escape-hatch entry ("Otra"), whose licenses must carry an
-- other_detail saying which one — enforced in licenses.Service.Create,
-- since a CHECK constraint can't reach across to license_types.is_other.
CREATE TABLE license_types (
    id       bigserial PRIMARY KEY,
    name     text NOT NULL UNIQUE,
    is_other boolean NOT NULL DEFAULT false
);

INSERT INTO license_types (name) VALUES
    ('A BASICA'), ('A BASICA Infantil'), ('A BASICA Juvenil'),
    ('A PLUS'), ('A PLUS Infantil'), ('A PLUS Juvenil'),
    ('B BASICA'), ('B BASICA Infantil'), ('B BASICA Juvenil'),
    ('B PLUS'), ('B PLUS Infantil'), ('B PLUS Juvenil'),
    ('B1 BASICA'), ('B1 BASICA Infantil'), ('B1 BASICA Juvenil'),
    ('B1 PLUS'), ('B1 PLUS Infantil'), ('B1 PLUS Juvenil'),
    ('ZERO'), ('ASEDEB'), ('FAE (Andaluza)'), ('FERM (Murciana)'), ('FMM (Montaña)');
INSERT INTO license_types (name, is_other) VALUES ('Otra', true);

-- A federative license ("licencia federativa"): a type chosen from the
-- catalog above, together with the date it was issued. Annual by
-- nature: valid from issued_date through December 31 of that same year,
-- computed in Go from issued_date rather than stored, since it's fully
-- determined by it.
CREATE TABLE licenses (
    id               bigserial PRIMARY KEY,
    user_id          bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    license_type_id  bigint NOT NULL REFERENCES license_types(id),
    other_detail     text,
    issued_date      date NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_licenses_user_id ON licenses(user_id);

-- +goose Down
DROP TABLE licenses;
DROP TABLE license_types;

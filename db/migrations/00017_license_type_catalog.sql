-- +goose Up
-- The catalog of federative license names, moved out of free text and
-- into the database: every (zone × modality × age bracket) combination
-- plus a handful of standalone names, so the list can grow (a new zone,
-- a new federation) with an INSERT instead of a code deploy. is_other
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

ALTER TABLE licenses ADD COLUMN license_type_id bigint REFERENCES license_types(id);
ALTER TABLE licenses ADD COLUMN other_detail text;

-- Backfill: match each existing free-text type against a catalog name;
-- anything that doesn't match (e.g. "FME") becomes "Otra" with the
-- original text preserved as other_detail, so no history is lost.
UPDATE licenses l SET license_type_id = lt.id
FROM license_types lt WHERE lt.name = l.type;

UPDATE licenses SET
    license_type_id = (SELECT id FROM license_types WHERE is_other),
    other_detail = type
WHERE license_type_id IS NULL;

ALTER TABLE licenses ALTER COLUMN license_type_id SET NOT NULL;
ALTER TABLE licenses DROP COLUMN type;

-- +goose Down
ALTER TABLE licenses ADD COLUMN type text;
UPDATE licenses l SET type = COALESCE(l.other_detail, lt.name)
FROM license_types lt WHERE lt.id = l.license_type_id;
ALTER TABLE licenses ALTER COLUMN type SET NOT NULL;
ALTER TABLE licenses DROP COLUMN license_type_id;
ALTER TABLE licenses DROP COLUMN other_detail;
DROP TABLE license_types;

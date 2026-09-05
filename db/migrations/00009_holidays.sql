-- +goose Up
-- Backs the activities calendar view: which days are club holidays, so
-- they can be marked alongside weekends (computed from the date itself,
-- no table needed for those).
CREATE TABLE holidays (
    id   bigserial PRIMARY KEY,
    date date NOT NULL UNIQUE,
    name text NOT NULL
);

-- Seeded with the fixed-date Spanish national holidays for 2026 — the
-- ones that fall on the same date every year regardless of the club's
-- autonomous community. Movable holidays (Viernes Santo...) and
-- regional/local ones aren't included here; add them (and future
-- years) with a plain INSERT, the same catalog-table pattern
-- material_categories/license_types already use — there's no API
-- endpoint to manage this list.
INSERT INTO holidays (date, name) VALUES
    ('2026-01-01', 'Año Nuevo'),
    ('2026-01-06', 'Epifanía del Señor'),
    ('2026-05-01', 'Fiesta del Trabajo'),
    ('2026-08-15', 'Asunción de la Virgen'),
    ('2026-10-12', 'Fiesta Nacional de España'),
    ('2026-11-01', 'Todos los Santos'),
    ('2026-12-06', 'Día de la Constitución'),
    ('2026-12-08', 'Inmaculada Concepción'),
    ('2026-12-25', 'Navidad');

-- +goose Down
DROP TABLE holidays;

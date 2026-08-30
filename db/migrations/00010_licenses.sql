-- +goose Up
-- A federative license ("licencia federativa"): zone (A, B, B1...) and
-- modality (BASICA, PLUS...) are free text rather than an enum or a
-- managed catalog — the club expects new ones to show up over time and
-- typing a new value shouldn't need a migration. Annual by nature: valid
-- from issued_date through December 31 of that same year, computed in
-- Go from issued_date rather than stored, since it's fully determined by
-- it.
CREATE TABLE licenses (
    id           bigserial PRIMARY KEY,
    user_id      bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    zone         text NOT NULL,
    modality     text NOT NULL,
    issued_date  date NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_licenses_user_id ON licenses(user_id);

-- +goose Down
DROP TABLE licenses;

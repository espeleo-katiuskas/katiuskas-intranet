-- +goose Up
-- How many municipal boundaries of an extract the club has no zone for, and which (their INE codes,
-- up to twenty): the sign that the list of municipalities of the club is behind the one of
-- OpenStreetMap (a new municipality, a merger) and that its elements are left without a zone. Each
-- load of the mirror writes it in its row, and the status of the system shows it.
ALTER TABLE osm_sync_runs
    ADD COLUMN unknown_zones integer NOT NULL DEFAULT 0,
    ADD COLUMN unknown_zone_codes text;

-- +goose Down
ALTER TABLE osm_sync_runs
    DROP COLUMN unknown_zone_codes,
    DROP COLUMN unknown_zones;

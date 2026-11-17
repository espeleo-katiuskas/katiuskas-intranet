-- +goose Up
-- The zone (the row of `areas`) each element of the OpenStreetMap mirror is in, so that elements
-- that share a name ("Cueva del Agua" has twenty) can be told apart: worked out by cmd/osmsync on
-- each load, from the boundaries of the same extract (in Spain, the municipality whose `ref:ine` is
-- the `code` of the zone), and empty for an element in no zone known (the sea, a country whose
-- zones are not in the catalogue).
--
-- It is the one foreign key that is ON DELETE SET NULL and not RESTRICT: what it holds is derived,
-- the next load works it out again, and an admin deleting a zone must not be stopped by the mirror.
-- The index is for that deletion, which has to find the elements that pointed at the zone.
ALTER TABLE osm_elements ADD COLUMN area_id bigint REFERENCES areas (id) ON DELETE SET NULL;
CREATE INDEX idx_osm_elements_area_id ON osm_elements (area_id);

-- +goose Down
DROP INDEX idx_osm_elements_area_id;
ALTER TABLE osm_elements DROP COLUMN area_id;

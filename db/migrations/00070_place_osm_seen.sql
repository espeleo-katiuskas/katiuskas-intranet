-- +goose Up
-- What OpenStreetMap said of its element the last time somebody linked the place to it or looked at what had
-- changed there: the tags and the point of the element, as a snapshot. A load of the mirror that changes the
-- element does not touch the place; the API compares this with the element as it is now, to tell a person what
-- changed (docs/osm-integration.md, section 8). Empty for a place that is not an element of OpenStreetMap.
ALTER TABLE places
    ADD COLUMN osm_seen jsonb,
    ADD CONSTRAINT places_osm_seen_needs_element CHECK (osm_seen IS NULL OR osm_type IS NOT NULL);

-- The places that were already linked start with the element as it is now.
UPDATE places p
SET osm_seen = jsonb_build_object('tags', e.tags, 'lat', e.lat, 'lon', e.lon)
FROM osm_elements e
WHERE e.osm_type = p.osm_type AND e.osm_id = p.osm_id;

-- +goose Down
ALTER TABLE places
    DROP CONSTRAINT places_osm_seen_needs_element,
    DROP COLUMN osm_seen;

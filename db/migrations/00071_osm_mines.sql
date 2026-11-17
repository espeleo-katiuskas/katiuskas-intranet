-- +goose Up
-- A fifth category of the OpenStreetMap mirror: mines (adits, shafts and mines that are no longer worked), which
-- the club explores like any other cavity: the place made from one starts as a cavity of the subtype Mina.
ALTER TABLE osm_elements
    DROP CONSTRAINT osm_elements_category_check,
    ADD CONSTRAINT osm_elements_category_check CHECK (category IN ('cave', 'canyon', 'mine', 'hut', 'via_ferrata'));

-- +goose Down
-- The mines go, and so do the links of the places that were one of them (the link is optional).
UPDATE places SET osm_type = NULL, osm_id = NULL, osm_seen = NULL
WHERE (osm_type, osm_id) IN (SELECT osm_type, osm_id FROM osm_elements WHERE category = 'mine');
DELETE FROM osm_elements WHERE category = 'mine';
ALTER TABLE osm_elements
    DROP CONSTRAINT osm_elements_category_check,
    ADD CONSTRAINT osm_elements_category_check CHECK (category IN ('cave', 'canyon', 'hut', 'via_ferrata'));

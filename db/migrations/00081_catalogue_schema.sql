-- +goose Up
-- The catalogue of places has a schema of its own: the places and their types, subtypes, geologies,
-- points, files and systems, the zones, and the mirror of OpenStreetMap. Moving a table takes its
-- indexes, constraints and sequences with it. The catalogue points at nothing outside its schema (the
-- keys to users went in 00080); the one foreign key that crosses the border is that of activity_places
-- to places, which stays as a backstop. The code names the tables with the schema (catalogue.places)
-- and nothing puts catalogue on the search path, so a query that crosses the border says so.
CREATE SCHEMA catalogue;

ALTER TABLE areas SET SCHEMA catalogue;
ALTER TABLE place_types SET SCHEMA catalogue;
ALTER TABLE place_subtypes SET SCHEMA catalogue;
ALTER TABLE place_geologies SET SCHEMA catalogue;
ALTER TABLE place_systems SET SCHEMA catalogue;
ALTER TABLE places SET SCHEMA catalogue;
ALTER TABLE place_points SET SCHEMA catalogue;
ALTER TABLE place_files SET SCHEMA catalogue;
ALTER TABLE osm_elements SET SCHEMA catalogue;
ALTER TABLE osm_sync_runs SET SCHEMA catalogue;

-- +goose Down
ALTER TABLE catalogue.osm_sync_runs SET SCHEMA public;
ALTER TABLE catalogue.osm_elements SET SCHEMA public;
ALTER TABLE catalogue.place_files SET SCHEMA public;
ALTER TABLE catalogue.place_points SET SCHEMA public;
ALTER TABLE catalogue.places SET SCHEMA public;
ALTER TABLE catalogue.place_systems SET SCHEMA public;
ALTER TABLE catalogue.place_geologies SET SCHEMA public;
ALTER TABLE catalogue.place_subtypes SET SCHEMA public;
ALTER TABLE catalogue.place_types SET SCHEMA public;
ALTER TABLE catalogue.areas SET SCHEMA public;

DROP SCHEMA catalogue;

-- +goose Up
-- The catalogue of places can be a project of its own, with its own database (Cuevas y simas), and then
-- the club's database has no places table to point at. An activity keeps the id of each place it goes to
-- and nothing else: that the place exists is asked of the catalogue when the link is made
-- (activities.Service), and the catalogue is told that the club uses it (references), so that it does not
-- delete it. The index on place_id stays: the counts of activities per place read it.
ALTER TABLE activity_places DROP CONSTRAINT activity_places_place_id_fkey;

-- +goose Down
ALTER TABLE activity_places
    ADD CONSTRAINT activity_places_place_id_fkey
    FOREIGN KEY (place_id) REFERENCES catalogue.places(id) ON DELETE RESTRICT;

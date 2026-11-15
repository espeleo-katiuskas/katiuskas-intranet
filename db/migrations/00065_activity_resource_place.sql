-- +goose Up
-- The place a resource of an activity's memoria is about, when it is about one: optional, and it has to be
-- one of the places the activity went to (the composite foreign key, which a NULL does not check). So a
-- place cannot be taken off an activity while an entry of its memoria is about it.
ALTER TABLE activity_resources ADD COLUMN place_id bigint;
ALTER TABLE activity_resources ADD CONSTRAINT activity_resources_place_fkey
    FOREIGN KEY (activity_id, place_id) REFERENCES activity_places (activity_id, place_id);
CREATE INDEX idx_activity_resources_place ON activity_resources (activity_id, place_id);

-- +goose Down
ALTER TABLE activity_resources DROP CONSTRAINT activity_resources_place_fkey;
DROP INDEX idx_activity_resources_place;
ALTER TABLE activity_resources DROP COLUMN place_id;

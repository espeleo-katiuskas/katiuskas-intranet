-- +goose Up
-- The foreign key that migration 00065 added (the place of an entry of an activity's memoria) was
-- left without ON DELETE RESTRICT, like every other foreign key has since migration 00049 (a test
-- checks it). It acted the same, since NO ACTION also refuses the deletion, but the rule is one for
-- all of them.
ALTER TABLE activity_resources
    DROP CONSTRAINT activity_resources_place_fkey,
    ADD CONSTRAINT activity_resources_place_fkey
        FOREIGN KEY (activity_id, place_id) REFERENCES activity_places (activity_id, place_id)
        ON DELETE RESTRICT;

-- +goose Down
ALTER TABLE activity_resources
    DROP CONSTRAINT activity_resources_place_fkey,
    ADD CONSTRAINT activity_resources_place_fkey
        FOREIGN KEY (activity_id, place_id) REFERENCES activity_places (activity_id, place_id);

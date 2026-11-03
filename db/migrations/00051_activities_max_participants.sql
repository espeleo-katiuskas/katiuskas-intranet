-- +goose Up
-- An optional limit on how many people go to an activity: the members signed up and the guests
-- together (checked in Go, which counts both under a lock of the activity's row).
ALTER TABLE activities ADD COLUMN max_participants integer;
ALTER TABLE activities ADD CONSTRAINT activities_max_participants_positive
    CHECK (max_participants IS NULL OR max_participants > 0);

-- +goose Down
ALTER TABLE activities DROP CONSTRAINT activities_max_participants_positive;
ALTER TABLE activities DROP COLUMN max_participants;

-- +goose Up
-- activity_participants.confirmed was in the first schema, always true: nothing ever set it to
-- anything else, and nothing read it. Whether somebody is coming is just being signed up.
ALTER TABLE activity_participants DROP COLUMN confirmed;

-- +goose Down
ALTER TABLE activity_participants ADD COLUMN confirmed boolean NOT NULL DEFAULT true;

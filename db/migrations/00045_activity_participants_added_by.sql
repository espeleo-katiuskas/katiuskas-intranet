-- +goose Up
-- Who signed each person up to an activity, and when. A person can be signed up by themselves or by
-- somebody else (any person in a public activity; in a private one, the organizer or somebody who is
-- already signed up), so it is worth keeping. For the people signed up before this was recorded the
-- organizer of the activity is taken as who did it, and the activity's own creation as when. Like
-- activities.leader_id, a person who has signed somebody up cannot be deleted.
ALTER TABLE activity_participants
    ADD COLUMN added_by bigint REFERENCES users(id),
    ADD COLUMN added_at timestamptz;
UPDATE activity_participants ap SET added_by = a.leader_id, added_at = a.created_at
    FROM activities a WHERE a.id = ap.activity_id;
ALTER TABLE activity_participants
    ALTER COLUMN added_by SET NOT NULL,
    ALTER COLUMN added_at SET NOT NULL,
    ALTER COLUMN added_at SET DEFAULT now();

-- +goose Down
ALTER TABLE activity_participants DROP COLUMN added_at, DROP COLUMN added_by;

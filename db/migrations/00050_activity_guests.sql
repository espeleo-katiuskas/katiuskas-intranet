-- +goose Up
-- People from outside the club who go to an activity. Each is a name and the person of the club who
-- invites them, who has to be signed up to the activity: the composite foreign key says so, and, like
-- every foreign key, it refuses (ON DELETE RESTRICT) to remove that person from the activity while
-- they still have guests.
CREATE TABLE activity_guests (
    id         bigserial PRIMARY KEY,
    activity_id bigint NOT NULL REFERENCES activities(id) ON DELETE RESTRICT,
    name       text NOT NULL,
    invited_by bigint NOT NULL,
    added_by   bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    added_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT activity_guests_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT activity_guests_invited_by_fkey FOREIGN KEY (activity_id, invited_by)
        REFERENCES activity_participants (activity_id, user_id) ON DELETE RESTRICT
);

-- The same person does not invite the same name twice to one activity.
CREATE UNIQUE INDEX activity_guests_unique_name ON activity_guests (activity_id, invited_by, lower(name));

-- +goose Down
DROP TABLE activity_guests;

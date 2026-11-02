-- +goose Up
-- activity_participants.role was in the first schema, always 'participant': nothing ever set it to
-- 'leader' and nothing depended on it. The organizer of an activity is activities.leader_id.
ALTER TABLE activity_participants DROP COLUMN role;
DROP TYPE participant_role;

-- +goose Down
CREATE TYPE participant_role AS ENUM ('leader', 'participant');
ALTER TABLE activity_participants ADD COLUMN role participant_role NOT NULL DEFAULT 'participant';

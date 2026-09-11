-- +goose Up
CREATE TYPE activity_type AS ENUM ('caving', 'canyoning', 'training', 'social', 'other');
CREATE TYPE activity_status AS ENUM ('planned', 'completed', 'cancelled');
CREATE TYPE activity_visibility AS ENUM ('public', 'private');
CREATE TYPE participant_role AS ENUM ('leader', 'participant');

CREATE TABLE activities (
    id                bigserial PRIMARY KEY,
    title             text NOT NULL,
    type              activity_type NOT NULL DEFAULT 'caving',
    start_date        date NOT NULL,
    end_date          date NOT NULL,
    description       text,
    status            activity_status NOT NULL DEFAULT 'planned',
    -- The organizer: always set, defaulting to whoever created the
    -- activity — admin/board can instead name someone else at creation
    -- time, or reassign it afterward (see activities.Handler.create/
    -- Handler.update; a plain member can't do either). The organizer is
    -- the only person besides admin/board who can edit or cancel it.
    leader_id         bigint NOT NULL REFERENCES users(id),
    -- public (the default): anyone can join themselves, and admin/board
    -- can add anyone else. private: only the organizer, board, or admin
    -- may add a participant at all, even to sign up themselves —
    -- enforced in activities.Handler.join, not here.
    visibility        activity_visibility NOT NULL DEFAULT 'public',
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT end_not_before_start CHECK (end_date >= start_date)
);

CREATE TABLE activity_participants (
    activity_id  bigint NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    user_id      bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role         participant_role NOT NULL DEFAULT 'participant',
    confirmed    boolean NOT NULL DEFAULT true,
    PRIMARY KEY (activity_id, user_id)
);

CREATE INDEX idx_activities_start_date ON activities(start_date);

-- +goose Down
DROP TABLE activity_participants;
DROP TABLE activities;
DROP TYPE participant_role;
DROP TYPE activity_visibility;
DROP TYPE activity_status;
DROP TYPE activity_type;

-- +goose Up
CREATE TYPE activity_type AS ENUM ('caving', 'training', 'outdoor', 'social', 'other');
CREATE TYPE activity_status AS ENUM ('planned', 'completed', 'cancelled');
CREATE TYPE participant_role AS ENUM ('leader', 'participant');

CREATE TABLE activities (
    id                bigserial PRIMARY KEY,
    title             text NOT NULL,
    type              activity_type NOT NULL DEFAULT 'caving',
    start_date        date NOT NULL,
    end_date          date,
    location          text NOT NULL,
    description       text,
    difficulty        smallint,
    max_participants  integer,
    status            activity_status NOT NULL DEFAULT 'planned',
    leader_id         bigint REFERENCES users(id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT end_not_before_start
        CHECK (end_date IS NULL OR end_date >= start_date)
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
DROP TYPE activity_status;
DROP TYPE activity_type;

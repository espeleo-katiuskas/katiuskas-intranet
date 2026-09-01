-- +goose Up
-- A memoria (write-up) is just this activity's own ordered list of
-- resources — there's no separate "memoria" row of its own, nothing to
-- create before the first resource can be added. A resource is either a
-- text entry (title + Markdown/plain-text description) or an uploaded
-- file (title + the file itself, stored on disk — see
-- config.ActivityResourcesRoot, internal/activityresources.Storage);
-- the CHECK constraints below keep each row's fields matching whichever
-- one it actually is.
CREATE TYPE activity_resource_type AS ENUM ('text', 'file');

CREATE TABLE activity_resources (
    id            bigserial PRIMARY KEY,
    activity_id   bigint NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    type          activity_resource_type NOT NULL,
    author_id     bigint NOT NULL REFERENCES users(id),
    title         text NOT NULL,
    -- text only.
    description   text,
    -- file only: the file's own bytes live on disk, keyed by this row's
    -- id — these three columns are just what's needed to serve them
    -- back out with the right name/type, and to show a size without
    -- opening the file.
    file_name     text,
    file_size     bigint,
    content_type  text,
    -- Where this resource sits on the memoria page — always a dense
    -- 1..N per activity, rewritten in full by
    -- Repository.Reorder whenever the order changes (see
    -- PUT /activities/{id}/resources/order); a fresh resource is
    -- appended after whatever the current highest position is.
    position      integer NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT description_only_for_text CHECK (
        type = 'text' OR description IS NULL
    ),
    CONSTRAINT description_required_for_text CHECK (
        type <> 'text' OR description IS NOT NULL
    ),
    CONSTRAINT file_fields_only_for_file CHECK (
        type = 'file' OR (file_name IS NULL AND file_size IS NULL AND content_type IS NULL)
    ),
    CONSTRAINT file_fields_required_for_file CHECK (
        type <> 'file' OR (file_name IS NOT NULL AND file_size IS NOT NULL AND content_type IS NOT NULL)
    )
);

CREATE INDEX idx_activity_resources_activity_id ON activity_resources(activity_id);

-- +goose Down
DROP TABLE activity_resources;
DROP TYPE activity_resource_type;

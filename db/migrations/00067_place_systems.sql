-- +goose Up
-- Systems: a group of cavities that are connected underground, which is one cave as far as the underground
-- goes although each entrance is a place of its own. A system has a name, a description and files (photos,
-- documents) of its own. A cavity is in at most one system (a connection found later is a matter of
-- moving the cavities), and only a cavity can be in one.
CREATE TABLE place_systems (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        text NOT NULL CHECK (btrim(name) <> ''),
    description text,
    created_by  bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_by  bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_place_systems_name ON place_systems (lower(name));

ALTER TABLE places ADD COLUMN system_id bigint REFERENCES place_systems (id) ON DELETE RESTRICT;
ALTER TABLE places ADD CONSTRAINT system_only_for_caves CHECK (kind = 'cave' OR system_id IS NULL);
CREATE INDEX idx_places_system_id ON places (system_id);

-- The files of a place or of a system, in the same table and the same directory on disk: the id of a row
-- is unique among all of them, so no two files share a path.
ALTER TABLE place_files ALTER COLUMN place_id DROP NOT NULL;
ALTER TABLE place_files ADD COLUMN system_id bigint REFERENCES place_systems (id) ON DELETE RESTRICT;
ALTER TABLE place_files ADD CONSTRAINT file_of_a_place_or_a_system CHECK ((place_id IS NULL) <> (system_id IS NULL));
CREATE INDEX idx_place_files_system_id ON place_files (system_id);

-- +goose Down
-- The files of the systems, and the systems, are lost.
DELETE FROM place_files WHERE system_id IS NOT NULL;
ALTER TABLE place_files DROP CONSTRAINT file_of_a_place_or_a_system;
DROP INDEX idx_place_files_system_id;
ALTER TABLE place_files DROP COLUMN system_id;
ALTER TABLE place_files ALTER COLUMN place_id SET NOT NULL;
DROP INDEX idx_places_system_id;
ALTER TABLE places DROP CONSTRAINT system_only_for_caves;
ALTER TABLE places DROP COLUMN system_id;
DROP TABLE place_systems;

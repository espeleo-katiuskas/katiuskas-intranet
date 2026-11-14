-- +goose Up
-- The files of a place: photos, scanned topographies, documents… Any member attaches one, with an
-- optional caption. The bytes are on disk (PLACE_FILES_ROOT), one file per row with the row's id as
-- its name, like the files of an item of the library; only the metadata is here.
CREATE TABLE place_files (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    place_id     bigint NOT NULL REFERENCES places(id) ON DELETE RESTRICT,
    -- What the person who attached it calls it (a caption); the name of the file is kept apart.
    title        text,
    file_name    text NOT NULL CHECK (btrim(file_name) <> ''),
    content_type text NOT NULL CHECK (btrim(content_type) <> ''),
    file_size    integer NOT NULL CHECK (file_size > 0),
    uploaded_by  bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_place_files_place_id ON place_files(place_id);
CREATE INDEX idx_place_files_uploaded_by ON place_files(uploaded_by);

-- +goose Down
DROP TABLE place_files;

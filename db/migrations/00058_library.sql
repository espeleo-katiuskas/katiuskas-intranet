-- +goose Up
-- The club's library (books, magazines, maps, topographies...; not the archive of documents, which
-- is a directory on disk): a catalogue of the items, their loans to members, the comments the
-- members write about them and the files attached to them (a cover and anything else: documents,
-- topographies, scans...). The bytes of the files are on disk (LIBRARY_FILES_ROOT), one file per
-- row of library_resources with the row's id as its name, like the files of an activity's memoria.

-- Who looks after the library. Not a system role: like `material`, a club that does not want the
-- distinction can delete it and use board instead.
INSERT INTO roles (name, description, is_system, is_automatic) VALUES
    ('biblioteca', 'Gestiona la biblioteca: los ejemplares y sus préstamos.', false, false);

-- What kind of thing an item is (a book, a magazine, a map...): a catalogue that whoever manages the
-- library edits. The names are unique ignoring case.
CREATE TABLE library_types (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text NOT NULL CHECK (btrim(name) <> ''),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_library_types_name ON library_types (lower(name));
INSERT INTO library_types (name) VALUES ('Libro'), ('Revista'), ('Mapa'), ('Topografía'), ('Otro');

-- A row is one copy of an item, the one that is lent: two copies of a title are two rows.
CREATE TABLE library_items (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type_id     bigint NOT NULL REFERENCES library_types(id) ON DELETE RESTRICT,
    title       text NOT NULL CHECK (btrim(title) <> ''),
    author      text,
    publisher   text,
    year        integer CHECK (year BETWEEN 1000 AND 2200),
    isbn        text,
    -- Where it is kept in the club's rooms (a shelf mark, a box...).
    location    text,
    description text,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_library_items_type_id ON library_items(type_id);

-- A loan to a member: the day it goes out and, once it is back, the day it came back. An item that
-- is out has a loan with no returned_at, and has at most one (the partial unique index).
CREATE TABLE library_loans (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    item_id     bigint NOT NULL REFERENCES library_items(id) ON DELETE RESTRICT,
    user_id     bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    lent_at     date NOT NULL DEFAULT CURRENT_DATE,
    returned_at date,
    recorded_by bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    notes       text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT library_loan_returned_after_lent CHECK (returned_at IS NULL OR returned_at >= lent_at)
);
CREATE UNIQUE INDEX idx_library_loans_one_open_per_item ON library_loans(item_id) WHERE returned_at IS NULL;
CREATE INDEX idx_library_loans_item_id ON library_loans(item_id);
CREATE INDEX idx_library_loans_user_id ON library_loans(user_id);

CREATE TABLE library_comments (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    item_id    bigint NOT NULL REFERENCES library_items(id) ON DELETE RESTRICT,
    user_id    bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    body       text NOT NULL CHECK (btrim(body) <> '' AND char_length(body) <= 2000),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_library_comments_item_id ON library_comments(item_id);
CREATE INDEX idx_library_comments_user_id ON library_comments(user_id);

-- The files of an item: its cover (at most one, the partial unique index) and any other resource.
CREATE TABLE library_resources (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    item_id      bigint NOT NULL REFERENCES library_items(id) ON DELETE RESTRICT,
    is_cover     boolean NOT NULL DEFAULT false,
    -- What the person who attached it calls it (a caption); the name of the file is kept apart.
    title        text,
    file_name    text NOT NULL CHECK (btrim(file_name) <> ''),
    content_type text NOT NULL CHECK (btrim(content_type) <> ''),
    file_size    integer NOT NULL CHECK (file_size > 0),
    uploaded_by  bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_library_resources_one_cover_per_item ON library_resources(item_id) WHERE is_cover;
CREATE INDEX idx_library_resources_item_id ON library_resources(item_id);
CREATE INDEX idx_library_resources_user_id ON library_resources(uploaded_by);

-- +goose Down
DROP TABLE library_resources;
DROP TABLE library_comments;
DROP TABLE library_loans;
DROP TABLE library_items;
DROP TABLE library_types;
DELETE FROM user_roles WHERE role_id = (SELECT id FROM roles WHERE name = 'biblioteca');
DELETE FROM roles WHERE name = 'biblioteca';

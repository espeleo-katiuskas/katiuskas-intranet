-- +goose Up
-- The catalogue of the places where the club goes: caves and shafts, canyons, via ferratas, huts...
-- A place has one main coordinate (in the place itself, so it always has one), a zone that says where it is
-- and any number of other points (where to park, where the approach starts, waypoints, each access...).

-- The zones of the world, as a tree of any depth: a country, and below it whatever its administrative
-- divisions are (in Spain a community, a province and a municipality; in Andorra a parish). Nothing in
-- the code knows the levels: `kind` is the text that names each one ("Provincia"), and a place points at
-- the most specific zone it is in, so its country, province... are its ancestors. The roots are the
-- states of the United Nations, all of them seeded (migration 00062); anybody adds a zone below another
-- (a territory that is not a state goes below the one that administers it), and an admin
-- renames, merges and deletes them. `norm_name` is the name without case, accents or punctuation, which
-- is what the search and the uniqueness of the names below a zone use, so "Valdemanco" and
-- "valdemanco" are the same. `code` is an official code when there is one: ISO 3166-1 alpha-2 for a
-- country, the INE's for a Spanish community, province or municipality.
CREATE TABLE areas (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    parent_id  bigint REFERENCES areas(id) ON DELETE RESTRICT,
    kind       text NOT NULL CHECK (btrim(kind) <> ''),
    name       text NOT NULL CHECK (btrim(name) <> ''),
    norm_name  text NOT NULL CHECK (norm_name <> ''),
    code       text CHECK (code IS NULL OR btrim(code) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT areas_not_its_own_parent CHECK (parent_id IS NULL OR parent_id <> id)
);
CREATE UNIQUE INDEX idx_areas_parent_name ON areas (COALESCE(parent_id, 0), norm_name);
CREATE INDEX idx_areas_parent_id ON areas (parent_id);
CREATE INDEX idx_areas_norm_name ON areas (norm_name text_pattern_ops);

-- What kind of place something is. The kind decides which fields the place has: a cave (a Cueva or a
-- Sima) has a depth and a horizontal length, and a cave or a canyon has a technical sheet (what is
-- missing to go through it). Cueva, Sima and Cañón are fixed; an admin adds the general types (the
-- ones of any other kind of place), like the types of material.
CREATE TABLE place_types (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text NOT NULL CHECK (btrim(name) <> ''),
    kind       text NOT NULL DEFAULT 'general' CHECK (kind IN ('cave', 'canyon', 'general')),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT place_types_id_kind_key UNIQUE (id, kind)
);
CREATE UNIQUE INDEX idx_place_types_name ON place_types (lower(name));
INSERT INTO place_types (name, kind) VALUES
    ('Cueva', 'cave'), ('Sima', 'cave'), ('Cañón', 'canyon'),
    ('Vía ferrata', 'general'), ('Refugio', 'general'), ('Otro', 'general');

-- What a cave is formed in (limestone, gypsum, ice, lava...): a catalogue that an admin edits, since there
-- are more kinds than anybody lists at once. Only a cave has one, and it is optional (a lot of caves have
-- never been looked at by a geologist). Nothing in the code depends on any of them.
CREATE TABLE place_geologies (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text NOT NULL CHECK (btrim(name) <> ''),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_place_geologies_name ON place_geologies (lower(name));
-- The usual ones first, then the others that have caves.
INSERT INTO place_geologies (name) VALUES
    ('Caliza'), ('Yeso'), ('Hielo'), ('Lava'), ('Dolomía'), ('Mármol'), ('Sal'), ('Arenisca'),
    ('Cuarcita'), ('Conglomerado'), ('Granito');

-- Coordinates are WGS84 latitude and longitude in degrees. The kind is the one of the place's type
-- (it is here so that the checks below can use it, as in material_items). Who created the place and
-- who changed it last are people; the API names them by login.
CREATE TABLE places (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type_id             bigint NOT NULL,
    kind                text NOT NULL,
    name                text NOT NULL CHECK (btrim(name) <> ''),
    -- Other names it is known by, as free text.
    alt_names           text,
    -- Under which conditions it can be visited: freely, only with a permit that has to be asked for,
    -- as a tourist attraction that has to be paid for, or not at all.
    state               text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'permit_required', 'tourist', 'closed')),
    -- Where it is: the most specific zone known (at least the country).
    area_id             bigint NOT NULL REFERENCES areas(id) ON DELETE RESTRICT,
    -- The main coordinate.
    lat                 double precision NOT NULL CHECK (lat BETWEEN -90 AND 90),
    lon                 double precision NOT NULL CHECK (lon BETWEEN -180 AND 180),
    -- Metres above sea level (below it, negative), when it is known: the Dead Sea is at -430 and the
    -- Himalayas go over 8,000.
    altitude_m          integer CHECK (altitude_m BETWEEN -500 AND 9000),
    -- How to get there, and a description: free text.
    access              text,
    description         text,
    -- Of a cave, in metres.
    depth_m             integer CHECK (depth_m >= 0),
    horizontal_length_m integer CHECK (horizontal_length_m >= 0),
    -- What a cave is formed in.
    geology_id          bigint REFERENCES place_geologies(id) ON DELETE RESTRICT,
    -- Of a cave or a canyon: what has to be installed to go through it.
    technical_sheet     text,
    created_by          bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_by          bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT places_type_fkey FOREIGN KEY (type_id, kind) REFERENCES place_types (id, kind) ON DELETE RESTRICT,
    CONSTRAINT measures_only_for_caves CHECK (kind = 'cave' OR (depth_m IS NULL AND horizontal_length_m IS NULL)),
    CONSTRAINT geology_only_for_caves CHECK (kind = 'cave' OR geology_id IS NULL),
    CONSTRAINT technical_sheet_only_for_caves_and_canyons CHECK (kind IN ('cave', 'canyon') OR technical_sheet IS NULL)
);
CREATE INDEX idx_places_type_id ON places (type_id);
CREATE INDEX idx_places_area_id ON places (area_id);
CREATE INDEX idx_places_geology_id ON places (geology_id);
CREATE INDEX idx_places_lat_lon ON places (lat, lon);

-- The other points of a place: where to leave the car, where the approach starts, a waypoint of the
-- way, each of the accesses... Nothing cascades: a place is deleted with its points in one transaction.
CREATE TABLE place_points (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    place_id   bigint NOT NULL REFERENCES places(id) ON DELETE RESTRICT,
    kind       text NOT NULL CHECK (kind IN ('parking', 'approach', 'waypoint', 'access', 'other')),
    name       text CHECK (name IS NULL OR btrim(name) <> ''),
    lat        double precision NOT NULL CHECK (lat BETWEEN -90 AND 90),
    lon        double precision NOT NULL CHECK (lon BETWEEN -180 AND 180),
    altitude_m integer CHECK (altitude_m BETWEEN -500 AND 9000),
    note       text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_place_points_place_id ON place_points (place_id);

-- +goose Down
DROP TABLE place_points;
DROP TABLE places;
DROP TABLE place_types;
DROP TABLE place_geologies;
DROP TABLE areas;

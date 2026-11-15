-- +goose Up
-- Changes to the places:
--   * Cueva and Sima become one type, Cavidad, and what each cavity is (cueva, sima, torca, mina,
--     abrigo...) is its subtype, a catalogue that an admin edits.
--   * The state (open, permit_required, tourist, closed) only means something for some places: the types
--     say whether they have one (`has_state`: Cavidad, Cañón and Vía ferrata), and a place of any other
--     has none.
--   * The other names of a place are a list, not a free text.

-- The subtypes of a cavity. Only a place whose type is of the cave kind has one, and then it has to.
CREATE TABLE place_subtypes (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text NOT NULL CHECK (btrim(name) <> ''),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_place_subtypes_name ON place_subtypes (lower(name));
INSERT INTO place_subtypes (name) VALUES ('Cueva'), ('Sima'), ('Torca'), ('Mina'), ('Abrigo');

-- Whether the places of a type have a state, fixed when the type is created like its kind is.
ALTER TABLE place_types ADD COLUMN has_state boolean NOT NULL DEFAULT false;
UPDATE place_types SET has_state = true WHERE kind IN ('cave', 'canyon') OR name = 'Vía ferrata';
ALTER TABLE place_types ADD CONSTRAINT place_types_id_kind_has_state_key UNIQUE (id, kind, has_state);
INSERT INTO place_types (name, kind, has_state) VALUES ('Cavidad', 'cave', true);

-- The places: the subtype, and (as the kind, so that the checks can use it) whether the type has a state.
ALTER TABLE places ADD COLUMN subtype_id bigint REFERENCES place_subtypes (id) ON DELETE RESTRICT;
ALTER TABLE places ADD COLUMN has_state boolean;
UPDATE places p SET has_state = t.has_state FROM place_types t WHERE t.id = p.type_id;
ALTER TABLE places ALTER COLUMN has_state SET NOT NULL;

-- What was a Cueva or a Sima is a Cavidad of that subtype.
UPDATE places SET type_id = (SELECT id FROM place_types WHERE name = 'Cavidad'),
    subtype_id = (SELECT id FROM place_subtypes WHERE name = 'Cueva')
    WHERE type_id = (SELECT id FROM place_types WHERE name = 'Cueva');
UPDATE places SET type_id = (SELECT id FROM place_types WHERE name = 'Cavidad'),
    subtype_id = (SELECT id FROM place_subtypes WHERE name = 'Sima')
    WHERE type_id = (SELECT id FROM place_types WHERE name = 'Sima');
DELETE FROM place_types WHERE name IN ('Cueva', 'Sima');

ALTER TABLE places DROP CONSTRAINT places_type_fkey;
ALTER TABLE places ADD CONSTRAINT places_type_fkey FOREIGN KEY (type_id, kind, has_state)
    REFERENCES place_types (id, kind, has_state) ON DELETE RESTRICT;
ALTER TABLE places ADD CONSTRAINT subtype_for_caves CHECK ((kind = 'cave') = (subtype_id IS NOT NULL));
CREATE INDEX idx_places_subtype_id ON places (subtype_id);

-- The state: none for a type without one. The places of those that had the default one lose it.
ALTER TABLE places ALTER COLUMN state DROP NOT NULL;
ALTER TABLE places ALTER COLUMN state DROP DEFAULT;
UPDATE places SET state = NULL WHERE NOT has_state;
ALTER TABLE places ADD CONSTRAINT state_for_types_with_state CHECK (has_state = (state IS NOT NULL));

-- The other names, as a list: the old text split at semicolons, commas and line breaks.
ALTER TABLE places ADD COLUMN other_names text[] NOT NULL DEFAULT '{}';
UPDATE places SET other_names = COALESCE((
    SELECT array_agg(name) FROM (
        SELECT btrim(part) AS name
        FROM regexp_split_to_table(replace(places.alt_names, chr(59), ','), '[,' || chr(10) || ']') AS part
    ) AS parts WHERE name <> ''
), '{}') WHERE alt_names IS NOT NULL;
ALTER TABLE places DROP COLUMN alt_names;
ALTER TABLE places RENAME COLUMN other_names TO alt_names;
ALTER TABLE places ADD CONSTRAINT alt_names_not_too_many CHECK (cardinality(alt_names) <= 50);

-- +goose Down
-- Best effort: Cueva and Sima come back, a cavity of any other subtype goes back as a Cueva, every place
-- has the state "open" if it had none, and the names are joined with "; ".
ALTER TABLE places DROP CONSTRAINT alt_names_not_too_many;
ALTER TABLE places ADD COLUMN old_alt_names text;
UPDATE places SET old_alt_names = array_to_string(alt_names, chr(59) || ' ') WHERE cardinality(alt_names) > 0;
ALTER TABLE places DROP COLUMN alt_names;
ALTER TABLE places RENAME COLUMN old_alt_names TO alt_names;

ALTER TABLE places DROP CONSTRAINT state_for_types_with_state;
UPDATE places SET state = 'open' WHERE state IS NULL;
ALTER TABLE places ALTER COLUMN state SET DEFAULT 'open';
ALTER TABLE places ALTER COLUMN state SET NOT NULL;

INSERT INTO place_types (name, kind, has_state) VALUES ('Cueva', 'cave', true), ('Sima', 'cave', true);
ALTER TABLE places DROP CONSTRAINT places_type_fkey;
ALTER TABLE places DROP CONSTRAINT subtype_for_caves;
UPDATE places SET type_id = (SELECT id FROM place_types WHERE name = 'Sima')
    WHERE subtype_id = (SELECT id FROM place_subtypes WHERE name = 'Sima');
UPDATE places SET type_id = (SELECT id FROM place_types WHERE name = 'Cueva')
    WHERE type_id = (SELECT id FROM place_types WHERE name = 'Cavidad');
DELETE FROM place_types WHERE name = 'Cavidad';
DROP INDEX idx_places_subtype_id;
ALTER TABLE places DROP COLUMN subtype_id;
ALTER TABLE places DROP COLUMN has_state;
ALTER TABLE place_types DROP CONSTRAINT place_types_id_kind_has_state_key;
ALTER TABLE places ADD CONSTRAINT places_type_fkey FOREIGN KEY (type_id, kind)
    REFERENCES place_types (id, kind) ON DELETE RESTRICT;
ALTER TABLE place_types DROP COLUMN has_state;
DROP TABLE place_subtypes;

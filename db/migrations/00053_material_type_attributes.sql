-- +goose Up
-- Attributes per type of material. An admin defines, for each general type, the attributes its
-- items carry (colour, closure, shape...): each is free text or one of a list of options, required
-- or optional. An item keeps its values in `attributes`, a jsonb object keyed by the attribute id:
-- a string for a text attribute, the option id (a number) for an enum one. Only attributes that
-- have a value are stored, never null or "".
--
-- A type also says whether the description of its items is required (it used to be for every
-- general item), and no two items may be the same: the unique index at the end.

CREATE TYPE material_attribute_type AS ENUM ('text', 'enum');

ALTER TABLE material_types ADD COLUMN description_required boolean NOT NULL DEFAULT true;

CREATE TABLE material_type_attributes (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type_id bigint NOT NULL,
    -- Always 'general': the fixed types (Cuerda, Saca) have their own columns, not attributes.
    kind material_kind NOT NULL DEFAULT 'general' CHECK (kind = 'general'),
    name text NOT NULL CHECK (btrim(name) <> ''),
    data_type material_attribute_type NOT NULL,
    required boolean NOT NULL DEFAULT false,
    -- The order in which the values of an item are written in its name.
    position integer NOT NULL,
    UNIQUE (type_id, name),
    FOREIGN KEY (type_id, kind) REFERENCES material_types (id, kind) ON DELETE RESTRICT
);

CREATE TABLE material_attribute_options (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    attribute_id bigint NOT NULL REFERENCES material_type_attributes (id) ON DELETE RESTRICT,
    label text NOT NULL CHECK (btrim(label) <> ''),
    UNIQUE (attribute_id, label)
);

ALTER TABLE material_items ADD COLUMN attributes jsonb NOT NULL DEFAULT '{}';
ALTER TABLE material_items ADD CONSTRAINT attributes_only_for_general
    CHECK (kind = 'general' OR attributes = '{}');

-- The description is required or not depending on the type, which the service checks.
ALTER TABLE material_items DROP CONSTRAINT general_requires_description;

-- If some already are the same, the migration stops and names them: they have to be merged by hand
-- first (one item with the sum of the stock, which is made of movements, not a column).
-- +goose StatementBegin
DO $$
DECLARE
    offending text;
BEGIN
    SELECT string_agg(ids, '; ') INTO offending FROM (
        SELECT string_agg(format('#%s', id), ', ' ORDER BY id) AS ids
        FROM material_items
        GROUP BY type_id, brand, model, code, description
        HAVING count(*) > 1
    ) groups;
    IF offending IS NOT NULL THEN
        RAISE EXCEPTION 'these groups of material items are exactly the same, merge them first: %', offending;
    END IF;
END
$$;
-- +goose StatementEnd

-- Two items cannot be the same: same type, make, model, code, description and attributes. The
-- quantity is not part of it (identical units are one item with a stock of more than one). The long
-- texts go in as their hash so that no row can be too big for the index.
CREATE UNIQUE INDEX material_items_identity_key ON material_items
    (type_id, brand, model, code, md5(description), md5(attributes::text)) NULLS NOT DISTINCT;

-- +goose Down
DROP INDEX material_items_identity_key;
UPDATE material_items SET description = '(sin descripción)' WHERE kind = 'general' AND description IS NULL;
ALTER TABLE material_items ADD CONSTRAINT general_requires_description
    CHECK (kind <> 'general' OR description IS NOT NULL);
ALTER TABLE material_items DROP CONSTRAINT attributes_only_for_general;
ALTER TABLE material_items DROP COLUMN attributes;
DROP TABLE material_attribute_options;
DROP TABLE material_type_attributes;
ALTER TABLE material_types DROP COLUMN description_required;
DROP TYPE material_attribute_type;

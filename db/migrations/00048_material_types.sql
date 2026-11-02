-- +goose Up
-- The type of a material item and its category become one thing: material_types. Two of them are
-- fixed and can be neither renamed nor deleted — Cuerda (kind 'rope') and Saca (kind 'bag'), whose
-- items carry the rope and bag fields; the rest (kind 'general', the old categories) are edited
-- freely. The item keeps its `kind` (the old `type` column), which the CHECK constraints hang from;
-- a composite foreign key keeps it consistent with its type.

ALTER TYPE material_item_type RENAME TO material_kind;
ALTER TABLE material_items RENAME COLUMN type TO kind;

ALTER TABLE material_categories RENAME TO material_types;
ALTER SEQUENCE material_categories_id_seq RENAME TO material_types_id_seq;
ALTER TABLE material_types RENAME CONSTRAINT material_categories_pkey TO material_types_pkey;
ALTER TABLE material_types RENAME CONSTRAINT material_categories_name_key TO material_types_name_key;

ALTER TABLE material_types ADD COLUMN kind material_kind NOT NULL DEFAULT 'general';

-- A category already called like a fixed type is renamed out of the way.
UPDATE material_types SET name = name || ' (general)' WHERE name IN ('Cuerda', 'Saca');
INSERT INTO material_types (name, kind) VALUES ('Cuerda', 'rope'), ('Saca', 'bag');

ALTER TABLE material_types ADD CONSTRAINT material_types_id_kind_key UNIQUE (id, kind);
-- There is exactly one fixed type of each kind.
CREATE UNIQUE INDEX material_types_fixed_kind_key ON material_types (kind) WHERE kind <> 'general';

ALTER TABLE material_items ADD COLUMN type_id bigint;
UPDATE material_items i SET type_id = t.id FROM material_types t
    WHERE i.kind <> 'general' AND t.kind = i.kind;
UPDATE material_items SET type_id = category_id WHERE kind = 'general';
ALTER TABLE material_items ALTER COLUMN type_id SET NOT NULL;

ALTER TABLE material_items DROP CONSTRAINT category_only_for_general;
ALTER TABLE material_items DROP CONSTRAINT general_requires_category;
ALTER TABLE material_items DROP COLUMN category_id;
ALTER TABLE material_items ADD CONSTRAINT material_items_type_fkey
    FOREIGN KEY (type_id, kind) REFERENCES material_types (id, kind);

-- +goose StatementBegin
CREATE FUNCTION material_types_guard_fixed() RETURNS trigger AS $$
BEGIN
    IF OLD.kind <> 'general' THEN
        RAISE EXCEPTION 'the material type % is fixed', OLD.name USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.kind <> OLD.kind THEN
        RAISE EXCEPTION 'the kind of a material type cannot change' USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER material_types_guard_fixed BEFORE UPDATE OR DELETE ON material_types
    FOR EACH ROW EXECUTE FUNCTION material_types_guard_fixed();

-- +goose Down
DROP TRIGGER material_types_guard_fixed ON material_types;
DROP FUNCTION material_types_guard_fixed();

ALTER TABLE material_items ADD COLUMN category_id bigint;
UPDATE material_items SET category_id = type_id WHERE kind = 'general';
ALTER TABLE material_items DROP CONSTRAINT material_items_type_fkey;
ALTER TABLE material_items DROP COLUMN type_id;
DELETE FROM material_types WHERE kind <> 'general';
UPDATE material_types SET name = left(name, length(name) - length(' (general)'))
    WHERE name IN ('Cuerda (general)', 'Saca (general)');
DROP INDEX material_types_fixed_kind_key;
ALTER TABLE material_types DROP CONSTRAINT material_types_id_kind_key;
ALTER TABLE material_types DROP COLUMN kind;

ALTER TABLE material_types RENAME CONSTRAINT material_types_name_key TO material_categories_name_key;
ALTER TABLE material_types RENAME CONSTRAINT material_types_pkey TO material_categories_pkey;
ALTER SEQUENCE material_types_id_seq RENAME TO material_categories_id_seq;
ALTER TABLE material_types RENAME TO material_categories;

ALTER TABLE material_items ADD CONSTRAINT material_items_category_id_fkey
    FOREIGN KEY (category_id) REFERENCES material_categories (id);
ALTER TABLE material_items ADD CONSTRAINT general_requires_category
    CHECK (kind <> 'general' OR category_id IS NOT NULL);
ALTER TABLE material_items ADD CONSTRAINT category_only_for_general
    CHECK (kind = 'general' OR category_id IS NULL);

ALTER TABLE material_items RENAME COLUMN kind TO type;
ALTER TYPE material_kind RENAME TO material_item_type;

-- +goose Up
-- The types of material have an order, set by an admin: it is the order of the "Tipo" list when an
-- item is created or edited, and of the items in the catalog. Cuerda and Saca can be moved too, so
-- the trigger that keeps them fixed now refuses to change their name, kind or description setting,
-- not any update.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION material_types_guard_fixed() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.kind <> OLD.kind THEN
        RAISE EXCEPTION 'the kind of a material type cannot change' USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF OLD.kind <> 'general' AND (TG_OP = 'DELETE' OR NEW.name <> OLD.name
            OR NEW.description_required <> OLD.description_required) THEN
        RAISE EXCEPTION 'the material type % is fixed', OLD.name USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

ALTER TABLE material_types ADD COLUMN position integer;
UPDATE material_types t SET position = o.pos FROM (
    SELECT id, row_number() OVER (ORDER BY kind = 'general', kind, name) - 1 AS pos FROM material_types
) o WHERE o.id = t.id;
ALTER TABLE material_types ALTER COLUMN position SET NOT NULL;

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION material_types_guard_fixed() RETURNS trigger AS $$
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
ALTER TABLE material_types DROP COLUMN position;

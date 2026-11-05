-- +goose Up
-- No two activities on the same start date with the same title (ignoring case and the spaces around
-- it): the pickers that list activities show the date and the title only, so two like that could
-- not be told apart.
CREATE UNIQUE INDEX activities_start_date_title_key ON activities (start_date, lower(btrim(title)));

-- An item's code is never empty and is not only digits: a listing writes it as "#<code>" and, for an
-- item with no code, "#<id>", so a code made of digits could be mistaken for an id.
-- +goose StatementBegin
DO $$
DECLARE
    offending text;
BEGIN
    -- A blank code is the same as none.
    UPDATE material_items SET code = NULL WHERE code IS NOT NULL AND btrim(code) = '';
    SELECT string_agg(format('#%s: %s', id, code), ', ') INTO offending
    FROM material_items WHERE code ~ '^[0-9]+$';
    IF offending IS NOT NULL THEN
        RAISE EXCEPTION 'these material items have a code made only of digits, change it first: %', offending;
    END IF;
END
$$;
-- +goose StatementEnd
ALTER TABLE material_items ADD CONSTRAINT material_items_code_not_empty
    CHECK (code IS NULL OR btrim(code) <> '');
ALTER TABLE material_items ADD CONSTRAINT material_items_code_not_only_digits
    CHECK (code IS NULL OR code !~ '^[0-9]+$');

-- +goose Down
ALTER TABLE material_items DROP CONSTRAINT material_items_code_not_only_digits;
ALTER TABLE material_items DROP CONSTRAINT material_items_code_not_empty;
DROP INDEX activities_start_date_title_key;

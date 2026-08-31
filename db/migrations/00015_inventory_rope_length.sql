-- +goose Up
ALTER TABLE inventory_items ADD COLUMN length_m integer;

-- Any rope that already exists predates this column; 60m is a common
-- standard length, so it's the least-wrong backfill — correct it by
-- hand afterward for the ones that aren't.
UPDATE inventory_items SET length_m = 60 WHERE item_type = 'rope';

ALTER TABLE inventory_items
    DROP CONSTRAINT rope_requires_rope_fields,
    DROP CONSTRAINT rope_fields_only_for_rope;

ALTER TABLE inventory_items
    ADD CONSTRAINT rope_requires_rope_fields CHECK (
        item_type <> 'rope' OR (
            rope_type IS NOT NULL AND diameter_mm IS NOT NULL AND
            manufacture_year IS NOT NULL AND length_m IS NOT NULL
        )
    ),
    ADD CONSTRAINT rope_fields_only_for_rope CHECK (
        item_type = 'rope' OR (
            rope_type IS NULL AND diameter_mm IS NULL AND
            manufacture_year IS NULL AND length_m IS NULL
        )
    );

-- +goose Down
ALTER TABLE inventory_items
    DROP CONSTRAINT rope_fields_only_for_rope,
    DROP CONSTRAINT rope_requires_rope_fields;

ALTER TABLE inventory_items
    ADD CONSTRAINT rope_requires_rope_fields CHECK (
        item_type <> 'rope' OR (rope_type IS NOT NULL AND diameter_mm IS NOT NULL AND manufacture_year IS NOT NULL)
    ),
    ADD CONSTRAINT rope_fields_only_for_rope CHECK (
        item_type = 'rope' OR (rope_type IS NULL AND diameter_mm IS NULL AND manufacture_year IS NULL)
    );

ALTER TABLE inventory_items DROP COLUMN length_m;

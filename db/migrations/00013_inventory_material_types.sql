-- +goose Up
-- Ropes and bags are individually tracked (their own code, always
-- total_quantity 1) with type-specific attributes; everything else
-- ("gear") stays a fungible stock item filed under an admin-managed
-- category, the same catalog pattern as license_types (migration
-- 00007) — new categories are an INSERT, not a deploy.
CREATE TYPE item_material_type AS ENUM ('rope', 'bag', 'gear');
CREATE TYPE rope_type AS ENUM ('caving', 'canyoning');

CREATE TABLE material_categories (
    id    bigserial PRIMARY KEY,
    name  text UNIQUE NOT NULL
);

INSERT INTO material_categories (name) VALUES
    ('Mosquetones'), ('Spits'), ('Placas'), ('Maillones'), ('Iluminación');

ALTER TABLE inventory_items
    ADD COLUMN item_type        item_material_type NOT NULL DEFAULT 'gear',
    ADD COLUMN brand            text,
    ADD COLUMN model            text,
    -- Whether an item can be loaned at all: unlike sale/rental (whether
    -- sale_price/rental_price_per_week is set says so already), a loan
    -- carries no price, so there's no other signal to derive this from.
    ADD COLUMN loanable         boolean NOT NULL DEFAULT true,
    ADD COLUMN rope_type        rope_type,
    ADD COLUMN diameter_mm      numeric(4,1),
    ADD COLUMN manufacture_year integer,
    ADD COLUMN capacity_liters  numeric(5,1),
    ADD COLUMN category_id      bigint REFERENCES material_categories(id);

ALTER TABLE inventory_items ALTER COLUMN item_type DROP DEFAULT;

-- name/category (free text) are replaced by brand/model and the
-- category catalog above.
ALTER TABLE inventory_items DROP COLUMN name;
ALTER TABLE inventory_items DROP COLUMN category;

ALTER TABLE inventory_items
    ADD CONSTRAINT rope_requires_rope_fields CHECK (
        item_type <> 'rope' OR (rope_type IS NOT NULL AND diameter_mm IS NOT NULL AND manufacture_year IS NOT NULL)
    ),
    ADD CONSTRAINT rope_fields_only_for_rope CHECK (
        item_type = 'rope' OR (rope_type IS NULL AND diameter_mm IS NULL AND manufacture_year IS NULL)
    ),
    ADD CONSTRAINT bag_requires_capacity CHECK (
        item_type <> 'bag' OR capacity_liters IS NOT NULL
    ),
    ADD CONSTRAINT capacity_only_for_bag CHECK (
        item_type = 'bag' OR capacity_liters IS NULL
    ),
    ADD CONSTRAINT gear_requires_category CHECK (
        item_type <> 'gear' OR category_id IS NOT NULL
    ),
    ADD CONSTRAINT category_only_for_gear CHECK (
        item_type = 'gear' OR category_id IS NULL
    ),
    -- Rope and bag are individually identified objects, not a labelled
    -- SKU: brand, model and a code are always known for them. gear may
    -- or may not have a known brand/model (some parts are generic).
    ADD CONSTRAINT rope_and_bag_require_brand_model CHECK (
        item_type = 'gear' OR (brand IS NOT NULL AND model IS NOT NULL)
    ),
    ADD CONSTRAINT rope_and_bag_require_code CHECK (
        item_type = 'gear' OR code IS NOT NULL
    ),
    -- description doubles as "observaciones" for rope/bag in the
    -- frontend — optional there, but required to identify a gear item
    -- since it may have no brand/model at all.
    ADD CONSTRAINT gear_requires_description CHECK (
        item_type <> 'gear' OR description IS NOT NULL
    ),
    ADD CONSTRAINT rope_and_bag_single_quantity CHECK (
        item_type = 'gear' OR total_quantity = 1
    );

-- +goose Down
ALTER TABLE inventory_items
    DROP CONSTRAINT rope_and_bag_single_quantity,
    DROP CONSTRAINT gear_requires_description,
    DROP CONSTRAINT rope_and_bag_require_code,
    DROP CONSTRAINT rope_and_bag_require_brand_model,
    DROP CONSTRAINT category_only_for_gear,
    DROP CONSTRAINT gear_requires_category,
    DROP CONSTRAINT capacity_only_for_bag,
    DROP CONSTRAINT bag_requires_capacity,
    DROP CONSTRAINT rope_fields_only_for_rope,
    DROP CONSTRAINT rope_requires_rope_fields;

ALTER TABLE inventory_items ADD COLUMN name text NOT NULL DEFAULT '';
ALTER TABLE inventory_items ALTER COLUMN name DROP DEFAULT;
ALTER TABLE inventory_items ADD COLUMN category text;

ALTER TABLE inventory_items
    DROP COLUMN category_id,
    DROP COLUMN capacity_liters,
    DROP COLUMN manufacture_year,
    DROP COLUMN diameter_mm,
    DROP COLUMN rope_type,
    DROP COLUMN loanable,
    DROP COLUMN model,
    DROP COLUMN brand,
    DROP COLUMN item_type;

DROP TABLE material_categories;
DROP TYPE rope_type;
DROP TYPE item_material_type;

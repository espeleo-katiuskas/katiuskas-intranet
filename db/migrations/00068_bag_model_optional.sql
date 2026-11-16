-- +goose Up
-- The model of a bag becomes optional: many bags have none that anybody knows. The brand stays required
-- for ropes and bags, and the model of a rope too; a model still needs a brand (model_needs_brand).
ALTER TABLE material_items
    DROP CONSTRAINT rope_and_bag_require_brand_model,
    ADD CONSTRAINT rope_requires_brand_model
        CHECK (kind <> 'rope' OR (brand IS NOT NULL AND model IS NOT NULL)),
    ADD CONSTRAINT bag_requires_brand
        CHECK (kind <> 'bag' OR brand IS NOT NULL);

-- +goose Down
-- A bag with no model gets a dash, since the old constraint asks for one.
UPDATE material_items SET model = '-' WHERE kind = 'bag' AND model IS NULL;
ALTER TABLE material_items
    DROP CONSTRAINT bag_requires_brand,
    DROP CONSTRAINT rope_requires_brand_model,
    ADD CONSTRAINT rope_and_bag_require_brand_model
        CHECK (kind = 'general' OR (brand IS NOT NULL AND model IS NOT NULL));

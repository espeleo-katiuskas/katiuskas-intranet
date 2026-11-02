-- +goose Up
-- A model is the model of a brand: it can only be given when the brand is. (Brand, model and code are
-- required for ropes and bags and optional for general material; this only ties the model to the brand.)
-- A general item that had a model and no brand keeps that text as its brand, so nothing is lost.
UPDATE material_items SET brand = model, model = NULL WHERE model IS NOT NULL AND brand IS NULL;
ALTER TABLE material_items ADD CONSTRAINT model_needs_brand CHECK (model IS NULL OR brand IS NOT NULL);

-- +goose Down
ALTER TABLE material_items DROP CONSTRAINT model_needs_brand;

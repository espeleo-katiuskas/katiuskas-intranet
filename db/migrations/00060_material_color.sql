-- +goose Up
-- The colour of a rope or a bag, as the club calls it ("azul", "rojo y negro"): optional, and only
-- for those two kinds. It names the item in the lists, so it can be searched.
ALTER TABLE material_items
    ADD COLUMN color text,
    ADD CONSTRAINT material_items_color_not_empty CHECK (color IS NULL OR btrim(color) <> ''),
    ADD CONSTRAINT color_only_for_rope_and_bag CHECK (kind IN ('rope', 'bag') OR color IS NULL);

-- +goose Down
ALTER TABLE material_items
    DROP CONSTRAINT color_only_for_rope_and_bag,
    DROP CONSTRAINT material_items_color_not_empty,
    DROP COLUMN color;

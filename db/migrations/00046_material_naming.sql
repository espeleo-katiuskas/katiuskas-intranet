-- +goose Up
-- One word for the club's material: "material". Everything of it used to be called a mix of
-- inventory, item and material (inventory_items, item_movements, item_material_type, ...). Now the
-- tables, the view and the types are material_*, an item's type is just `type`, and the type
-- "gear" is "general" (as the interface says: "Material general"). Nothing else changes.
ALTER TABLE inventory_items RENAME TO material_items;
ALTER TABLE item_movements RENAME TO material_movements;
ALTER VIEW inventory_item_quantities RENAME TO material_item_quantities;

ALTER TYPE item_material_type RENAME TO material_item_type;
ALTER TYPE material_item_type RENAME VALUE 'gear' TO 'general';
ALTER TYPE item_status RENAME TO material_item_status;
ALTER TYPE movement_type RENAME TO material_movement_type;
ALTER TYPE movement_status RENAME TO material_movement_status;

ALTER TABLE material_items RENAME COLUMN item_type TO type;

ALTER SEQUENCE inventory_items_id_seq RENAME TO material_items_id_seq;
ALTER SEQUENCE item_movements_id_seq RENAME TO material_movements_id_seq;
ALTER INDEX idx_item_movements_item_id RENAME TO idx_material_movements_item_id;
ALTER INDEX idx_item_movements_user_id RENAME TO idx_material_movements_user_id;

-- Renaming a constraint that has an index (primary key, unique) renames the index too.
ALTER TABLE material_items RENAME CONSTRAINT inventory_items_pkey TO material_items_pkey;
ALTER TABLE material_items RENAME CONSTRAINT inventory_items_code_key TO material_items_code_key;
ALTER TABLE material_items RENAME CONSTRAINT inventory_items_category_id_fkey TO material_items_category_id_fkey;
ALTER TABLE material_items RENAME CONSTRAINT gear_requires_category TO general_requires_category;
ALTER TABLE material_items RENAME CONSTRAINT category_only_for_gear TO category_only_for_general;
ALTER TABLE material_items RENAME CONSTRAINT gear_requires_description TO general_requires_description;
ALTER TABLE material_movements RENAME CONSTRAINT item_movements_pkey TO material_movements_pkey;
ALTER TABLE material_movements RENAME CONSTRAINT item_movements_activity_id_fkey TO material_movements_activity_id_fkey;
ALTER TABLE material_movements RENAME CONSTRAINT item_movements_item_id_fkey TO material_movements_item_id_fkey;
ALTER TABLE material_movements RENAME CONSTRAINT item_movements_recorded_by_fkey TO material_movements_recorded_by_fkey;
ALTER TABLE material_movements RENAME CONSTRAINT item_movements_user_id_fkey TO material_movements_user_id_fkey;

-- +goose Down
ALTER TABLE material_movements RENAME CONSTRAINT material_movements_user_id_fkey TO item_movements_user_id_fkey;
ALTER TABLE material_movements RENAME CONSTRAINT material_movements_recorded_by_fkey TO item_movements_recorded_by_fkey;
ALTER TABLE material_movements RENAME CONSTRAINT material_movements_item_id_fkey TO item_movements_item_id_fkey;
ALTER TABLE material_movements RENAME CONSTRAINT material_movements_activity_id_fkey TO item_movements_activity_id_fkey;
ALTER TABLE material_movements RENAME CONSTRAINT material_movements_pkey TO item_movements_pkey;
ALTER TABLE material_items RENAME CONSTRAINT general_requires_description TO gear_requires_description;
ALTER TABLE material_items RENAME CONSTRAINT category_only_for_general TO category_only_for_gear;
ALTER TABLE material_items RENAME CONSTRAINT general_requires_category TO gear_requires_category;
ALTER TABLE material_items RENAME CONSTRAINT material_items_category_id_fkey TO inventory_items_category_id_fkey;
ALTER TABLE material_items RENAME CONSTRAINT material_items_code_key TO inventory_items_code_key;
ALTER TABLE material_items RENAME CONSTRAINT material_items_pkey TO inventory_items_pkey;

ALTER INDEX idx_material_movements_user_id RENAME TO idx_item_movements_user_id;
ALTER INDEX idx_material_movements_item_id RENAME TO idx_item_movements_item_id;
ALTER SEQUENCE material_movements_id_seq RENAME TO item_movements_id_seq;
ALTER SEQUENCE material_items_id_seq RENAME TO inventory_items_id_seq;

ALTER TABLE material_items RENAME COLUMN type TO item_type;

ALTER TYPE material_movement_status RENAME TO movement_status;
ALTER TYPE material_movement_type RENAME TO movement_type;
ALTER TYPE material_item_status RENAME TO item_status;
ALTER TYPE material_item_type RENAME VALUE 'general' TO 'gear';
ALTER TYPE material_item_type RENAME TO item_material_type;

ALTER VIEW material_item_quantities RENAME TO inventory_item_quantities;
ALTER TABLE material_movements RENAME TO item_movements;
ALTER TABLE material_items RENAME TO inventory_items;

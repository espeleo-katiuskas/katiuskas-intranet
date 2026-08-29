-- +goose Up
ALTER TABLE inventory_items RENAME COLUMN rental_price_per_day TO rental_price_per_week;

ALTER TABLE item_transactions ADD COLUMN activity_id bigint REFERENCES activities(id);

-- Loans and rentals always happen as part of an activity; a sale doesn't
-- (the material leaves the club's inventory for good, activity or not).
ALTER TABLE item_transactions ADD CONSTRAINT loan_and_rental_require_activity
    CHECK (type = 'sale' OR activity_id IS NOT NULL);

-- Not a system role: a club that doesn't want this distinction can just
-- delete it and keep using board for material transactions instead.
INSERT INTO roles (name, description, is_system)
VALUES ('material', 'Gestiona préstamos, alquileres y ventas de material.', false)
ON CONFLICT (name) DO NOTHING;

-- +goose Down
DELETE FROM roles WHERE name = 'material' AND is_system = false;
ALTER TABLE item_transactions DROP CONSTRAINT loan_and_rental_require_activity;
ALTER TABLE item_transactions DROP COLUMN activity_id;
ALTER TABLE inventory_items RENAME COLUMN rental_price_per_week TO rental_price_per_day;

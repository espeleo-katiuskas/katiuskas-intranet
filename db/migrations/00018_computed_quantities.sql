-- +goose Up
-- total_quantity/available_quantity stop being columns anyone writes —
-- they're now always derived from item_movements, so the two can never
-- drift apart the way a stored, manually-updated pair can. Existing
-- items need a synthetic initial 'acquisition' movement first, so their
-- current stock survives the switch instead of resetting to zero.
INSERT INTO item_movements (item_id, type, quantity, transacted_at, status)
SELECT id, 'acquisition', total_quantity, COALESCE(acquired_at, CURRENT_DATE), 'completed'
FROM inventory_items;

ALTER TABLE inventory_items DROP COLUMN total_quantity;
ALTER TABLE inventory_items DROP COLUMN available_quantity;
ALTER TABLE inventory_items DROP COLUMN acquired_at;

-- total_quantity: everything ever acquired, minus everything that left
-- for good (sold, lost, abandoned, retired). available_quantity is the
-- same, further reduced by whatever's currently out on an unreturned
-- loan or rental.
CREATE VIEW inventory_item_quantities AS
SELECT
    i.id AS item_id,
    COALESCE(SUM(CASE
        WHEN m.type = 'acquisition' THEN m.quantity
        WHEN m.type IN ('sale', 'loss', 'abandonment', 'retirement') THEN -m.quantity
        ELSE 0
    END), 0) AS total_quantity,
    COALESCE(SUM(CASE
        WHEN m.type = 'acquisition' THEN m.quantity
        WHEN m.type IN ('sale', 'loss', 'abandonment', 'retirement') THEN -m.quantity
        WHEN m.type IN ('loan', 'rental') AND m.returned_at IS NULL THEN -m.quantity
        ELSE 0
    END), 0) AS available_quantity
FROM inventory_items i
LEFT JOIN item_movements m ON m.item_id = i.id
GROUP BY i.id;

-- +goose Down
ALTER TABLE inventory_items ADD COLUMN total_quantity integer NOT NULL DEFAULT 1;
ALTER TABLE inventory_items ADD COLUMN available_quantity integer NOT NULL DEFAULT 1;
ALTER TABLE inventory_items ADD COLUMN acquired_at date;

UPDATE inventory_items i SET
    total_quantity = COALESCE(q.total_quantity, 0),
    available_quantity = COALESCE(q.available_quantity, 0),
    acquired_at = (
        SELECT MIN(m.transacted_at) FROM item_movements m
        WHERE m.item_id = i.id AND m.type = 'acquisition'
    )
FROM inventory_item_quantities q
WHERE q.item_id = i.id;

DROP VIEW inventory_item_quantities;

ALTER TABLE inventory_items ADD CONSTRAINT available_within_total
    CHECK (available_quantity BETWEEN 0 AND total_quantity);
ALTER TABLE inventory_items ADD CONSTRAINT rope_and_bag_single_quantity
    CHECK (item_type = 'gear' OR total_quantity = 1);

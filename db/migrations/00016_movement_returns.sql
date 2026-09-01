-- +goose Up
-- A loan or rental can come back in pieces (30 mosquetones lent for a
-- trip, 10 returned one day, 20 the next) — one row per return event,
-- rather than item_movements.returned_at trying to represent the whole
-- history in a single date. item_movements.returned_at keeps meaning
-- "the date this movement was fully settled" (all of it returned, or
-- never settled at all: still NULL), set once the sum of its returns
-- reaches its quantity.
CREATE TABLE movement_returns (
    id           bigserial PRIMARY KEY,
    movement_id  bigint NOT NULL REFERENCES item_movements(id),
    quantity     integer NOT NULL CHECK (quantity > 0),
    returned_at  date NOT NULL DEFAULT CURRENT_DATE,
    recorded_by  bigint REFERENCES users(id)
);
CREATE INDEX idx_movement_returns_movement_id ON movement_returns(movement_id);

-- Every loan/rental already fully returned under the old (single
-- returned_at, no partial history) mechanism needs a matching return
-- row of its own, or the view below would see no movement_returns for
-- it and wrongly treat it as still fully outstanding.
INSERT INTO movement_returns (movement_id, quantity, returned_at, recorded_by)
SELECT id, quantity, returned_at, recorded_by
FROM item_movements
WHERE type IN ('loan', 'rental') AND returned_at IS NOT NULL;

-- available_quantity used to treat a loan/rental as "fully out" until
-- returned_at was set, then "fully back" the moment it was — an
-- all-or-nothing toggle that can't represent 10 of 30 having come
-- back. It now subtracts however much of each one is still
-- outstanding (quantity minus whatever's been returned so far),
-- which is 0 once a movement is fully settled either way.
DROP VIEW inventory_item_quantities;
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
        WHEN m.type IN ('loan', 'rental') THEN -(m.quantity - COALESCE(r.returned_quantity, 0))
        ELSE 0
    END), 0) AS available_quantity
FROM inventory_items i
LEFT JOIN item_movements m ON m.item_id = i.id
LEFT JOIN (
    SELECT movement_id, SUM(quantity) AS returned_quantity
    FROM movement_returns
    GROUP BY movement_id
) r ON r.movement_id = m.id
GROUP BY i.id;

-- +goose Down
DROP VIEW inventory_item_quantities;
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

DROP TABLE movement_returns;

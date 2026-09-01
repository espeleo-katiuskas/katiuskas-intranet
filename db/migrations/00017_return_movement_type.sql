-- +goose Up
-- movement_returns turned out to be more bookkeeping than the club
-- actually needs: a return is just another movement, symmetric with
-- acquisition (both add stock back) rather than a satellite table with
-- its own status/returned_at tracking. A rental still has to be
-- returned all at once (returned_at/status keep meaning exactly what
-- they did before movement_returns existed) — only loan gets the new
-- 'return' type, always tied to the activity it's for, validated
-- against how much of that item is still outstanding *for that
-- activity* (not linked to any one specific loan row).
ALTER TABLE item_movements DROP CONSTRAINT activity_required_for_loan;
ALTER TABLE item_movements DROP CONSTRAINT price_only_for_rental_and_sale;
ALTER TABLE item_movements DROP CONSTRAINT price_required_for_rental_and_sale;
ALTER TABLE item_movements DROP CONSTRAINT recipient_only_for_loan_rental_sale;
ALTER TABLE item_movements DROP CONSTRAINT recipient_required_for_rental;
ALTER TABLE item_movements DROP CONSTRAINT returnable_only_for_loan_and_rental;

-- Depends on item_movements.type, so it has to go before that column's
-- type can change — recreated (with the new logic) at the end.
DROP VIEW inventory_item_quantities;

CREATE TYPE movement_type_new AS ENUM (
    'loan', 'rental', 'sale', 'acquisition', 'loss', 'abandonment', 'retirement', 'return'
);
ALTER TABLE item_movements ALTER COLUMN type TYPE movement_type_new USING type::text::movement_type_new;
DROP TYPE movement_type;
ALTER TYPE movement_type_new RENAME TO movement_type;

-- Every loan already fully returned via movement_returns becomes a
-- real 'return' movement of its own, dated the same as that return
-- event and tied to the same activity as the loan it came from.
INSERT INTO item_movements (item_id, type, quantity, activity_id, transacted_at, status)
SELECT m.item_id, 'return', mr.quantity, m.activity_id, mr.returned_at, 'completed'
FROM movement_returns mr
JOIN item_movements m ON m.id = mr.movement_id
WHERE m.type = 'loan';

-- A loan no longer tracks its own return state at all — like
-- acquisition/sale/loss/abandonment/retirement, it's just a completed
-- record the moment it's made; what's still outstanding is now always
-- computed from loan/return movements together (see the view below).
UPDATE item_movements SET returned_at = NULL, status = 'completed' WHERE type = 'loan';

DROP TABLE movement_returns;

ALTER TABLE item_movements ADD CONSTRAINT activity_required_for_loan_and_return CHECK (
    type NOT IN ('loan', 'return') OR activity_id IS NOT NULL
);
ALTER TABLE item_movements ADD CONSTRAINT price_only_for_rental_and_sale CHECK (
    type IN ('rental', 'sale') OR price IS NULL
);
ALTER TABLE item_movements ADD CONSTRAINT price_required_for_rental_and_sale CHECK (
    type NOT IN ('rental', 'sale') OR price IS NOT NULL
);
ALTER TABLE item_movements ADD CONSTRAINT recipient_only_for_loan_rental_sale_and_return CHECK (
    type IN ('loan', 'rental', 'sale', 'return') OR user_id IS NULL
);
ALTER TABLE item_movements ADD CONSTRAINT recipient_required_for_rental CHECK (
    type <> 'rental' OR user_id IS NOT NULL
);
-- Only rental still closes with a single all-or-nothing returned_at —
-- loan's "returned" state is now the sum of separate 'return' rows,
-- not a flag on the loan itself.
ALTER TABLE item_movements ADD CONSTRAINT returnable_only_for_rental CHECK (
    type = 'rental' OR returned_at IS NULL
);

-- Back to a flat signed sum, same mechanism as every other type: a
-- return adds back to available_quantity exactly like an acquisition
-- does, rather than being resolved against a specific loan row.
-- Rental is unchanged (still an all-or-nothing returned_at gate).
-- outstanding_loan_quantity is the new bit: how much of this item is
-- currently out on some loan, across every activity, with nothing
-- (yet) returned against it — what "material prestado" is built from.
CREATE VIEW inventory_item_quantities AS
SELECT
    i.id AS item_id,
    COALESCE(SUM(CASE
        WHEN m.type = 'acquisition' THEN m.quantity
        WHEN m.type IN ('sale', 'loss', 'abandonment', 'retirement') THEN -m.quantity
        ELSE 0
    END), 0) AS total_quantity,
    COALESCE(SUM(CASE
        WHEN m.type IN ('acquisition', 'return') THEN m.quantity
        WHEN m.type IN ('sale', 'loss', 'abandonment', 'retirement', 'loan') THEN -m.quantity
        WHEN m.type = 'rental' AND m.returned_at IS NULL THEN -m.quantity
        ELSE 0
    END), 0) AS available_quantity,
    COALESCE(SUM(CASE
        WHEN m.type = 'loan' THEN m.quantity
        WHEN m.type = 'return' THEN -m.quantity
        ELSE 0
    END), 0) AS outstanding_loan_quantity
FROM inventory_items i
LEFT JOIN item_movements m ON m.item_id = i.id
GROUP BY i.id;

-- +goose Down
-- Depends on item_movements.type — same as the Up side, has to go
-- before the column's type can change back.
DROP VIEW inventory_item_quantities;

ALTER TABLE item_movements DROP CONSTRAINT returnable_only_for_rental;
ALTER TABLE item_movements DROP CONSTRAINT recipient_required_for_rental;
ALTER TABLE item_movements DROP CONSTRAINT recipient_only_for_loan_rental_sale_and_return;
ALTER TABLE item_movements DROP CONSTRAINT price_required_for_rental_and_sale;
ALTER TABLE item_movements DROP CONSTRAINT price_only_for_rental_and_sale;
ALTER TABLE item_movements DROP CONSTRAINT activity_required_for_loan_and_return;

CREATE TABLE movement_returns (
    id           bigserial PRIMARY KEY,
    movement_id  bigint NOT NULL REFERENCES item_movements(id),
    quantity     integer NOT NULL CHECK (quantity > 0),
    returned_at  date NOT NULL DEFAULT CURRENT_DATE,
    recorded_by  bigint REFERENCES users(id)
);
CREATE INDEX idx_movement_returns_movement_id ON movement_returns(movement_id);

-- 'return' rows can't be represented in the old enum at all — this is
-- lossy (their history doesn't come back as movement_returns rows
-- either), same as any down-migration whose new type simply can't
-- exist in the old schema.
DELETE FROM item_movements WHERE type = 'return';

CREATE TYPE movement_type_old AS ENUM (
    'loan', 'rental', 'sale', 'acquisition', 'loss', 'abandonment', 'retirement'
);
ALTER TABLE item_movements ALTER COLUMN type TYPE movement_type_old USING type::text::movement_type_old;
DROP TYPE movement_type;
ALTER TYPE movement_type_old RENAME TO movement_type;

ALTER TABLE item_movements ADD CONSTRAINT returnable_only_for_loan_and_rental CHECK (
    type IN ('loan', 'rental') OR returned_at IS NULL
);
ALTER TABLE item_movements ADD CONSTRAINT recipient_required_for_rental CHECK (
    type <> 'rental' OR user_id IS NOT NULL
);
ALTER TABLE item_movements ADD CONSTRAINT recipient_only_for_loan_rental_sale CHECK (
    type IN ('loan', 'rental', 'sale') OR user_id IS NULL
);
ALTER TABLE item_movements ADD CONSTRAINT price_required_for_rental_and_sale CHECK (
    type NOT IN ('rental', 'sale') OR price IS NOT NULL
);
ALTER TABLE item_movements ADD CONSTRAINT price_only_for_rental_and_sale CHECK (
    type IN ('rental', 'sale') OR price IS NULL
);
ALTER TABLE item_movements ADD CONSTRAINT activity_required_for_loan CHECK (
    type <> 'loan' OR activity_id IS NOT NULL
);

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

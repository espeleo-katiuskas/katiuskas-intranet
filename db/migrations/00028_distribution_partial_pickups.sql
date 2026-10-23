-- +goose Up
-- A row of units can be collected in part, as it can be paid in part:
-- picked_up_quantity says how many of its quantity were handed out, and
-- picked_up_at and who recorded it / who got it are those of the last
-- delivery, there exactly when something was handed out. When the article has
-- numbers, each delivered item is one of the row's identifiers, which says
-- when it was handed out: a number assigned but not delivered is still on the
-- shelf, and only those assigned can be delivered. What is returned goes back
-- to the pool: its number is deleted and the row gets smaller.
ALTER TABLE distribution_units ADD COLUMN picked_up_quantity integer NOT NULL DEFAULT 0;
UPDATE distribution_units SET picked_up_quantity = quantity WHERE picked_up_at IS NOT NULL;
ALTER TABLE distribution_units
    ADD CONSTRAINT picked_up_quantity_range CHECK (picked_up_quantity BETWEEN 0 AND quantity),
    ADD CONSTRAINT picked_up_quantity_matches_mark CHECK ((picked_up_quantity > 0) = (picked_up_at IS NOT NULL));

ALTER TABLE distribution_unit_identifiers ADD COLUMN delivered_at timestamptz;
UPDATE distribution_unit_identifiers i SET delivered_at = n.picked_up_at
FROM distribution_units n WHERE n.id = i.unit_id AND n.picked_up_at IS NOT NULL;

-- +goose Down
ALTER TABLE distribution_unit_identifiers DROP COLUMN delivered_at;
-- A row delivered in part counts as not delivered.
ALTER TABLE distribution_units
    DROP CONSTRAINT picked_up_quantity_matches_mark,
    DROP CONSTRAINT picked_up_quantity_range;
UPDATE distribution_units SET picked_up_at = NULL, pickup_recorded_by_id = NULL, picked_up_by_id = NULL
WHERE picked_up_quantity < quantity;
ALTER TABLE distribution_units DROP COLUMN picked_up_quantity;

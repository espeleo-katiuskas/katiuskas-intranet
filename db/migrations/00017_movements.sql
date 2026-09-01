-- +goose Up
-- "Transaction" was a bad name for this table: an accounting module will
-- eventually need that word for ledger entries, and this table is really
-- about material moving in and out of the club's hands. Renaming it also
-- widens transaction_type beyond loan/rental/sale to cover stock-level
-- changes with no recipient: acquisition (new stock in), loss/abandonment
-- (stock permanently gone with no sale behind it), and retirement (gear
-- taken out of service). Enum values can't be added and used inside the
-- same transaction, so the type is swapped rather than ALTERed in place —
-- the same pattern migration 00014 used for bag_type.
ALTER TABLE item_transactions DROP CONSTRAINT loan_and_rental_require_activity;
ALTER TABLE item_transactions DROP CONSTRAINT loan_has_no_price;
ALTER TABLE item_transactions DROP CONSTRAINT rental_and_sale_require_price;
ALTER TABLE item_transactions DROP CONSTRAINT sale_has_no_return;

CREATE TYPE movement_type AS ENUM (
    'loan', 'rental', 'sale', 'acquisition', 'loss', 'abandonment', 'retirement'
);
ALTER TABLE item_transactions ALTER COLUMN type TYPE movement_type USING type::text::movement_type;
DROP TYPE transaction_type;

ALTER TYPE transaction_status RENAME TO movement_status;

ALTER TABLE item_transactions RENAME TO item_movements;
ALTER TABLE item_movements RENAME CONSTRAINT item_transactions_pkey TO item_movements_pkey;
ALTER TABLE item_movements RENAME CONSTRAINT item_transactions_item_id_fkey TO item_movements_item_id_fkey;
ALTER TABLE item_movements RENAME CONSTRAINT item_transactions_user_id_fkey TO item_movements_user_id_fkey;
ALTER TABLE item_movements RENAME CONSTRAINT item_transactions_recorded_by_fkey TO item_movements_recorded_by_fkey;
ALTER TABLE item_movements RENAME CONSTRAINT item_transactions_activity_id_fkey TO item_movements_activity_id_fkey;
ALTER INDEX idx_item_transactions_item_id RENAME TO idx_item_movements_item_id;
ALTER INDEX idx_item_transactions_user_id RENAME TO idx_item_movements_user_id;

-- Acquisition/loss/abandonment/retirement have no recipient — nobody
-- "receives" a retired rope. Loan/rental/sale keep requiring one.
ALTER TABLE item_movements ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE item_movements ADD COLUMN notes text;

ALTER TABLE item_movements ADD CONSTRAINT recipient_required_for_loan_rental_sale CHECK (
    type NOT IN ('loan', 'rental', 'sale') OR user_id IS NOT NULL
);
ALTER TABLE item_movements ADD CONSTRAINT activity_required_for_loan_and_rental CHECK (
    type NOT IN ('loan', 'rental') OR activity_id IS NOT NULL
);
ALTER TABLE item_movements ADD CONSTRAINT price_required_for_rental_and_sale CHECK (
    type NOT IN ('rental', 'sale') OR price IS NOT NULL
);
ALTER TABLE item_movements ADD CONSTRAINT price_only_for_rental_and_sale CHECK (
    type IN ('rental', 'sale') OR price IS NULL
);
ALTER TABLE item_movements ADD CONSTRAINT returnable_only_for_loan_and_rental CHECK (
    type IN ('loan', 'rental') OR returned_at IS NULL
);

-- lost/abandoned join the already-unused 'retired' as statuses a
-- movement can drive an item to — see Repository.CreateMovement.
CREATE TYPE item_status_new AS ENUM (
    'available', 'on_loan', 'rented', 'sold', 'lost', 'abandoned', 'maintenance', 'retired'
);
ALTER TABLE inventory_items ALTER COLUMN status DROP DEFAULT;
ALTER TABLE inventory_items ALTER COLUMN status TYPE item_status_new USING status::text::item_status_new;
DROP TYPE item_status;
ALTER TYPE item_status_new RENAME TO item_status;
ALTER TABLE inventory_items ALTER COLUMN status SET DEFAULT 'available';

-- +goose Down
ALTER TABLE item_movements DROP CONSTRAINT returnable_only_for_loan_and_rental;
ALTER TABLE item_movements DROP CONSTRAINT price_only_for_rental_and_sale;
ALTER TABLE item_movements DROP CONSTRAINT price_required_for_rental_and_sale;
ALTER TABLE item_movements DROP CONSTRAINT activity_required_for_loan_and_rental;
ALTER TABLE item_movements DROP CONSTRAINT recipient_required_for_loan_rental_sale;

ALTER TABLE item_movements DROP COLUMN notes;
ALTER TABLE item_movements ALTER COLUMN user_id SET NOT NULL;

ALTER INDEX idx_item_movements_user_id RENAME TO idx_item_transactions_user_id;
ALTER INDEX idx_item_movements_item_id RENAME TO idx_item_transactions_item_id;
ALTER TABLE item_movements RENAME CONSTRAINT item_movements_activity_id_fkey TO item_transactions_activity_id_fkey;
ALTER TABLE item_movements RENAME CONSTRAINT item_movements_recorded_by_fkey TO item_transactions_recorded_by_fkey;
ALTER TABLE item_movements RENAME CONSTRAINT item_movements_user_id_fkey TO item_transactions_user_id_fkey;
ALTER TABLE item_movements RENAME CONSTRAINT item_movements_item_id_fkey TO item_transactions_item_id_fkey;
ALTER TABLE item_movements RENAME CONSTRAINT item_movements_pkey TO item_transactions_pkey;
ALTER TABLE item_movements RENAME TO item_transactions;

ALTER TYPE movement_status RENAME TO transaction_status;

CREATE TYPE transaction_type AS ENUM ('loan', 'rental', 'sale');
DELETE FROM item_transactions WHERE type NOT IN ('loan', 'rental', 'sale');
ALTER TABLE item_transactions ALTER COLUMN type TYPE transaction_type USING type::text::transaction_type;
DROP TYPE movement_type;

ALTER TABLE item_transactions ADD CONSTRAINT sale_has_no_return CHECK (
    type <> 'sale' OR returned_at IS NULL
);
ALTER TABLE item_transactions ADD CONSTRAINT rental_and_sale_require_price CHECK (
    type = 'loan' OR price IS NOT NULL
);
ALTER TABLE item_transactions ADD CONSTRAINT loan_has_no_price CHECK (
    type <> 'loan' OR price IS NULL
);
ALTER TABLE item_transactions ADD CONSTRAINT loan_and_rental_require_activity CHECK (
    type = 'sale' OR activity_id IS NOT NULL
);

CREATE TYPE item_status_old AS ENUM ('available', 'on_loan', 'rented', 'sold', 'maintenance', 'retired');
UPDATE inventory_items SET status = 'available' WHERE status IN ('lost', 'abandoned');
ALTER TABLE inventory_items ALTER COLUMN status DROP DEFAULT;
ALTER TABLE inventory_items ALTER COLUMN status TYPE item_status_old USING status::text::item_status_old;
DROP TYPE item_status;
ALTER TYPE item_status_old RENAME TO item_status;
ALTER TABLE inventory_items ALTER COLUMN status SET DEFAULT 'available';

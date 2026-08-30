-- +goose Up
CREATE TYPE item_status AS ENUM ('available', 'on_loan', 'rented', 'sold', 'maintenance', 'retired');
CREATE TYPE transaction_type AS ENUM ('loan', 'rental', 'sale');
CREATE TYPE transaction_status AS ENUM ('active', 'completed', 'overdue', 'cancelled');

CREATE TABLE inventory_items (
    id                     bigserial PRIMARY KEY,
    name                   text NOT NULL,
    description            text,
    category               text,
    code                   text UNIQUE,
    total_quantity         integer NOT NULL DEFAULT 1,
    available_quantity     integer NOT NULL DEFAULT 1,
    status                 item_status NOT NULL DEFAULT 'available',
    acquired_at            date,
    rental_price_per_week  numeric(10,2),
    sale_price             numeric(10,2),
    CONSTRAINT available_within_total
        CHECK (available_quantity BETWEEN 0 AND total_quantity)
);

CREATE TABLE item_transactions (
    id             bigserial PRIMARY KEY,
    item_id        bigint NOT NULL REFERENCES inventory_items(id),
    user_id        bigint NOT NULL REFERENCES users(id),
    type           transaction_type NOT NULL,
    quantity       integer NOT NULL DEFAULT 1,
    transacted_at  timestamptz NOT NULL DEFAULT now(),
    due_date       date,
    returned_at    date,
    price          numeric(10,2),
    status         transaction_status NOT NULL DEFAULT 'active',
    recorded_by    bigint REFERENCES users(id),
    -- Loans and rentals always happen as part of an activity; a sale
    -- doesn't (the material leaves the club's inventory for good,
    -- activity or not).
    activity_id    bigint REFERENCES activities(id),
    CONSTRAINT sale_has_no_return CHECK (
        type <> 'sale' OR (due_date IS NULL AND returned_at IS NULL)
    ),
    CONSTRAINT rental_and_sale_require_price CHECK (
        type = 'loan' OR price IS NOT NULL
    ),
    CONSTRAINT loan_and_rental_require_activity CHECK (
        type = 'sale' OR activity_id IS NOT NULL
    )
);

CREATE INDEX idx_item_transactions_item_id ON item_transactions(item_id);
CREATE INDEX idx_item_transactions_user_id ON item_transactions(user_id);

-- +goose Down
DROP TABLE item_transactions;
DROP TABLE inventory_items;
DROP TYPE transaction_status;
DROP TYPE transaction_type;
DROP TYPE item_status;

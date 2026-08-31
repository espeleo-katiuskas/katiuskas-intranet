-- +goose Up
-- transacted_at becomes a plain date (whoever records the transaction
-- can backdate it — a loan handed out yesterday shouldn't have to be
-- logged as happening right now), defaulting to today. due_date is
-- dropped: the club doesn't track an expected return date, only
-- whether/when material actually came back (returned_at).
ALTER TABLE item_transactions DROP CONSTRAINT sale_has_no_return;
ALTER TABLE item_transactions DROP COLUMN due_date;
ALTER TABLE item_transactions ADD CONSTRAINT sale_has_no_return CHECK (
    type <> 'sale' OR returned_at IS NULL
);

ALTER TABLE item_transactions ALTER COLUMN transacted_at TYPE date USING transacted_at::date;
ALTER TABLE item_transactions ALTER COLUMN transacted_at SET DEFAULT CURRENT_DATE;

-- A loan is always free — price must be null for one, same as it must
-- be set for a rental or sale (rental_and_sale_require_price, migration
-- 00006).
ALTER TABLE item_transactions ADD CONSTRAINT loan_has_no_price CHECK (
    type <> 'loan' OR price IS NULL
);

-- +goose Down
ALTER TABLE item_transactions DROP CONSTRAINT loan_has_no_price;

ALTER TABLE item_transactions ALTER COLUMN transacted_at TYPE timestamptz USING transacted_at::timestamptz;
ALTER TABLE item_transactions ALTER COLUMN transacted_at SET DEFAULT now();

ALTER TABLE item_transactions DROP CONSTRAINT sale_has_no_return;
ALTER TABLE item_transactions ADD COLUMN due_date date;
ALTER TABLE item_transactions ADD CONSTRAINT sale_has_no_return CHECK (
    type <> 'sale' OR (due_date IS NULL AND returned_at IS NULL)
);

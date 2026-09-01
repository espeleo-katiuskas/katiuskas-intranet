-- +goose Up
-- A loan's recipient becomes optional too — now only rental still
-- requires one. Loan's activity_id stays required (activity_required_
-- for_loan, migration 00013, untouched).
ALTER TABLE item_movements DROP CONSTRAINT recipient_required_for_loan_and_rental;
ALTER TABLE item_movements ADD CONSTRAINT recipient_required_for_rental CHECK (
    type <> 'rental' OR user_id IS NOT NULL
);

-- +goose Down
ALTER TABLE item_movements DROP CONSTRAINT recipient_required_for_rental;
ALTER TABLE item_movements ADD CONSTRAINT recipient_required_for_loan_and_rental CHECK (
    type NOT IN ('loan', 'rental') OR user_id IS NOT NULL
);

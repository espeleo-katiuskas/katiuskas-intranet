-- +goose Up
-- A rental doesn't have to be tied to a specific activity the way a
-- loan always is — only loan still requires one.
ALTER TABLE item_movements DROP CONSTRAINT activity_required_for_loan_and_rental;
ALTER TABLE item_movements ADD CONSTRAINT activity_required_for_loan CHECK (
    type <> 'loan' OR activity_id IS NOT NULL
);

-- +goose Down
ALTER TABLE item_movements DROP CONSTRAINT activity_required_for_loan;
ALTER TABLE item_movements ADD CONSTRAINT activity_required_for_loan_and_rental CHECK (
    type NOT IN ('loan', 'rental') OR activity_id IS NOT NULL
);

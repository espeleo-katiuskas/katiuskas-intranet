-- +goose Up
-- Loan and rental still always need a recipient. Sale's recipient
-- becomes optional (a sale doesn't always have to be tied to a socio).
-- Acquisition, loss, abandonment and retirement never get one at all —
-- previously allowed (so a loss/abandonment could optionally name who
-- was carrying the item), but that turned out to be more flexibility
-- than wanted.
ALTER TABLE item_movements DROP CONSTRAINT recipient_required_for_loan_rental_sale;

ALTER TABLE item_movements ADD CONSTRAINT recipient_required_for_loan_and_rental CHECK (
    type NOT IN ('loan', 'rental') OR user_id IS NOT NULL
);
ALTER TABLE item_movements ADD CONSTRAINT recipient_only_for_loan_rental_sale CHECK (
    type IN ('loan', 'rental', 'sale') OR user_id IS NULL
);

-- +goose Down
ALTER TABLE item_movements DROP CONSTRAINT recipient_only_for_loan_rental_sale;
ALTER TABLE item_movements DROP CONSTRAINT recipient_required_for_loan_and_rental;

ALTER TABLE item_movements ADD CONSTRAINT recipient_required_for_loan_rental_sale CHECK (
    type NOT IN ('loan', 'rental', 'sale') OR user_id IS NOT NULL
);

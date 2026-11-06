-- +goose Up
-- A sale or a rental of material is recorded in the accounting, as the payment of a distribution is:
-- money goes into the asset account it was paid in and out of the income account marked here (one
-- for rentals and one for sales: at most one account each, partial unique indexes), and the
-- movement points at the transaction it wrote. A movement is the only one that has a given
-- transaction.
ALTER TABLE accounts ADD COLUMN material_rental boolean NOT NULL DEFAULT false;
ALTER TABLE accounts ADD COLUMN material_sale boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX idx_accounts_material_rental ON accounts(material_rental) WHERE material_rental;
CREATE UNIQUE INDEX idx_accounts_material_sale ON accounts(material_sale) WHERE material_sale;

ALTER TABLE material_movements ADD COLUMN transaction_id bigint REFERENCES transactions(id) ON DELETE RESTRICT;
ALTER TABLE material_movements ADD CONSTRAINT transaction_only_for_rental_and_sale
    CHECK (transaction_id IS NULL OR type IN ('rental', 'sale'));
CREATE UNIQUE INDEX idx_material_movements_transaction ON material_movements(transaction_id)
    WHERE transaction_id IS NOT NULL;

-- +goose Down
DROP INDEX idx_material_movements_transaction;
ALTER TABLE material_movements DROP CONSTRAINT transaction_only_for_rental_and_sale;
ALTER TABLE material_movements DROP COLUMN transaction_id;
DROP INDEX idx_accounts_material_sale;
DROP INDEX idx_accounts_material_rental;
ALTER TABLE accounts DROP COLUMN material_sale;
ALTER TABLE accounts DROP COLUMN material_rental;

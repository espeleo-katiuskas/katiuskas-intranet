-- +goose Up
-- The payments of a distribution are recorded in the accounting. Each
-- distribution has an account its payments go to (account_id) — the one marked as
-- the default for distributions when it doesn't name its own — and when a payment
-- is marked, a transaction is made between the asset account it was paid in and
-- that one. The payments in the list of payments point at the transaction that
-- recorded them (null for those from before, which have none), and that
-- transaction can't be deleted while one does.
ALTER TABLE accounts ADD COLUMN distribution_default boolean NOT NULL DEFAULT false;
-- At most one account is the default.
CREATE UNIQUE INDEX idx_accounts_distribution_default ON accounts(distribution_default) WHERE distribution_default;

ALTER TABLE distributions ADD COLUMN account_id bigint REFERENCES accounts(id);

ALTER TABLE distribution_payments ADD COLUMN transaction_id bigint REFERENCES transactions(id);
CREATE INDEX idx_distribution_payments_transaction_id ON distribution_payments(transaction_id);

-- +goose Down
DROP INDEX idx_distribution_payments_transaction_id;
ALTER TABLE distribution_payments DROP COLUMN transaction_id;
ALTER TABLE distributions DROP COLUMN account_id;
DROP INDEX idx_accounts_distribution_default;
ALTER TABLE accounts DROP COLUMN distribution_default;

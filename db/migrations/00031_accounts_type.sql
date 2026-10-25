-- +goose Up
-- What an account is mainly for: asset (its balance is money the club really
-- has), income (used mainly to record what comes in) or expense (mainly what
-- goes out). Optional: a grouping account, or one that is none of these, has
-- none. More kinds can be added to the enum later.
CREATE TYPE account_type AS ENUM ('asset', 'income', 'expense');
ALTER TABLE accounts ADD COLUMN type account_type;

-- +goose Down
ALTER TABLE accounts DROP COLUMN type;
DROP TYPE account_type;

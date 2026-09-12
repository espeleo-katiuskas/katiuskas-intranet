-- +goose Up
-- Not every account needs a code of its own — a purely organizational
-- grouping node (created just to gather a handful of real accounts
-- under one report heading, say) might never get one, unlike a real
-- ledger account that follows the club's own numbering convention (see
-- migration 00012_accounting.sql's own "570 Caja" example). The UNIQUE
-- constraint on code is untouched: Postgres already treats every NULL
-- as distinct from every other for uniqueness purposes, so any number
-- of accounts can each have no code without colliding.
ALTER TABLE accounts ALTER COLUMN code DROP NOT NULL;

-- +goose Down
ALTER TABLE accounts ALTER COLUMN code SET NOT NULL;

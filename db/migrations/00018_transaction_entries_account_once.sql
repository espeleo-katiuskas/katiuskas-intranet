-- +goose Up
-- Each account appears at most once per transaction: its single entry's
-- amount already says everything that moved through it.
ALTER TABLE transaction_entries
    ADD CONSTRAINT transaction_entries_account_once UNIQUE (transaction_id, account_id);

-- +goose Down
ALTER TABLE transaction_entries DROP CONSTRAINT transaction_entries_account_once;

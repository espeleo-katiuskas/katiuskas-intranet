-- +goose Up
-- Documents (tickets, invoices) attached to a transaction. This table only holds the metadata; the
-- bytes are on disk (TRANSACTION_FILES_ROOT), one file per row with the row's id as its name, like
-- the files of an activity's memoria. A transaction that still has files can not be deleted (no ON
-- DELETE action: the files have to be removed first), the same way one that paid for a license can not.
CREATE TABLE transaction_files (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transaction_id bigint NOT NULL REFERENCES transactions(id),
    file_name      text NOT NULL CHECK (btrim(file_name) <> ''),
    content_type   text NOT NULL CHECK (btrim(content_type) <> ''),
    file_size      integer NOT NULL CHECK (file_size > 0),
    uploaded_by    bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_transaction_files_transaction_id ON transaction_files(transaction_id);

-- +goose Down
DROP TABLE transaction_files;

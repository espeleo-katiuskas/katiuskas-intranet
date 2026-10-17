-- +goose Up
-- A distribution has five states, each one a different thing people can do:
-- draft (being prepared, editable, only admin/board see it), open (members ask
-- for units and choose their options), closed (nothing changes any more, the
-- material isn't there yet), delivering (the units are being handed out) and
-- done. This replaces ordered and received, which named things that happen
-- outside the app: what "ordered" added to "closed" was nothing the app does
-- differently, and "received" is just when delivering can begin.
ALTER TYPE distribution_status RENAME TO distribution_status_old;
CREATE TYPE distribution_status AS ENUM ('draft', 'open', 'closed', 'delivering', 'done');
ALTER TABLE distributions ALTER COLUMN status DROP DEFAULT;
ALTER TABLE distributions ALTER COLUMN status TYPE distribution_status USING (
    CASE status::text
        WHEN 'ordered' THEN 'closed'
        WHEN 'received' THEN 'delivering'
        ELSE status::text
    END
)::distribution_status;
ALTER TABLE distributions ALTER COLUMN status SET DEFAULT 'draft';
DROP TYPE distribution_status_old;

-- +goose Down
ALTER TYPE distribution_status RENAME TO distribution_status_old;
CREATE TYPE distribution_status AS ENUM ('draft', 'open', 'closed', 'ordered', 'received', 'done');
ALTER TABLE distributions ALTER COLUMN status DROP DEFAULT;
ALTER TABLE distributions ALTER COLUMN status TYPE distribution_status USING (
    CASE status::text WHEN 'delivering' THEN 'received' ELSE status::text END
)::distribution_status;
ALTER TABLE distributions ALTER COLUMN status SET DEFAULT 'draft';
DROP TYPE distribution_status_old;

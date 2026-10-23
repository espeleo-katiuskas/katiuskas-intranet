-- +goose Up
-- Units can be handed out while a distribution is still open — the order often
-- arrives before the asking has ended — so "delivering" is no longer a state of
-- its own: handing out is possible while it is open or closed. A distribution
-- that was delivering had stopped taking requests, which is what closed means.
ALTER TYPE distribution_status RENAME TO distribution_status_old;
CREATE TYPE distribution_status AS ENUM ('draft', 'open', 'closed', 'done');
ALTER TABLE distributions ALTER COLUMN status DROP DEFAULT;
ALTER TABLE distributions ALTER COLUMN status TYPE distribution_status USING (
    CASE status::text WHEN 'delivering' THEN 'closed' ELSE status::text END
)::distribution_status;
ALTER TABLE distributions ALTER COLUMN status SET DEFAULT 'draft';
DROP TYPE distribution_status_old;

-- +goose Down
-- A closed distribution stays closed: there is no telling which ones were
-- delivering.
ALTER TYPE distribution_status RENAME TO distribution_status_old;
CREATE TYPE distribution_status AS ENUM ('draft', 'open', 'closed', 'delivering', 'done');
ALTER TABLE distributions ALTER COLUMN status DROP DEFAULT;
ALTER TABLE distributions ALTER COLUMN status TYPE distribution_status USING status::text::distribution_status;
ALTER TABLE distributions ALTER COLUMN status SET DEFAULT 'draft';
DROP TYPE distribution_status_old;

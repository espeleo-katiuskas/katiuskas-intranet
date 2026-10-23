-- +goose Up
-- A row of units can be paid in part: paid_quantity says how many of its
-- quantity have been paid (paid_at and paid_marked_by_id stay as when and by
-- whom the last payment was marked, and are set exactly when something is
-- paid). Asking for more of something already paid doesn't make the new units
-- paid, and the same person can now ask for the same article several times: a
-- request adds to the row of that article that is still to be collected, or
-- makes a new one, so nothing has to be edited.
ALTER TABLE distribution_units ADD COLUMN paid_quantity integer NOT NULL DEFAULT 0;
UPDATE distribution_units SET paid_quantity = quantity WHERE paid_at IS NOT NULL;
ALTER TABLE distribution_units
    ADD CONSTRAINT paid_quantity_range CHECK (paid_quantity BETWEEN 0 AND quantity),
    ADD CONSTRAINT paid_quantity_matches_mark CHECK ((paid_quantity > 0) = (paid_at IS NOT NULL));

DROP INDEX idx_distribution_units_article;
CREATE INDEX idx_distribution_units_article ON distribution_units(article_id, beneficiary_id) WHERE article_id IS NOT NULL;

-- +goose Down
-- Fails while somebody has two rows of the same article: there is no telling
-- which one to keep.
DROP INDEX idx_distribution_units_article;
CREATE UNIQUE INDEX idx_distribution_units_article
    ON distribution_units(article_id, beneficiary_id) WHERE article_id IS NOT NULL;
-- A row paid in part counts as unpaid.
ALTER TABLE distribution_units
    DROP CONSTRAINT paid_quantity_matches_mark,
    DROP CONSTRAINT paid_quantity_range;
UPDATE distribution_units SET paid_at = NULL, paid_marked_by_id = NULL WHERE paid_quantity < quantity;
ALTER TABLE distribution_units DROP COLUMN paid_quantity;

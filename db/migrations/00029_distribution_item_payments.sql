-- +goose Up
-- For an article with numbers, payment is per item as delivery is: only an
-- item with a number (an assigned identifier) can be paid, and which ones are.
-- paid_at / paid_marked_by_id of the identifier say when and by whom; the row's
-- paid_quantity is then the number of its paid identifiers. Rows of articles
-- without numbers keep counting.
ALTER TABLE distribution_unit_identifiers
    ADD COLUMN paid_at timestamptz,
    ADD COLUMN paid_marked_by_id bigint REFERENCES users(id);

-- What was paid in a row of an article with numbers goes to its first numbers; a
-- row that had more paid than numbers assigned (nothing says which units those
-- were) is left with as many as have a number.
UPDATE distribution_unit_identifiers i SET paid_at = n.paid_at, paid_marked_by_id = n.paid_marked_by_id
FROM distribution_units n
WHERE n.id = i.unit_id AND n.paid_quantity > 0
  AND i.id IN (
    SELECT x.id FROM distribution_unit_identifiers x WHERE x.unit_id = n.id ORDER BY x.id LIMIT n.paid_quantity
  );
UPDATE distribution_units n SET
    paid_quantity = (SELECT count(*) FROM distribution_unit_identifiers i WHERE i.unit_id = n.id AND i.paid_at IS NOT NULL)
WHERE n.article_id IN (SELECT id FROM distribution_articles WHERE has_identifiers);
UPDATE distribution_units SET paid_at = NULL, paid_marked_by_id = NULL WHERE paid_quantity = 0;

-- +goose Down
ALTER TABLE distribution_unit_identifiers DROP COLUMN paid_marked_by_id, DROP COLUMN paid_at;

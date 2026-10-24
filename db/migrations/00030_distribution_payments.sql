-- +goose Up
-- The payments of a distribution as they happened, one entry each: when, who
-- marked it, whose order it was for (the person who paid), what and how many
-- units, how much money, and which numbers. Taking a payment off writes the same
-- entry with the signs changed, so the running total of a distribution is the sum
-- up to any point. unit_id goes null if the order is deleted; the entry stays.
CREATE TABLE distribution_payments (
    id               bigserial PRIMARY KEY,
    distribution_id  bigint NOT NULL REFERENCES distributions(id) ON DELETE CASCADE,
    unit_id          bigint REFERENCES distribution_units(id) ON DELETE SET NULL,
    beneficiary_id   bigint NOT NULL REFERENCES users(id),
    description      text NOT NULL,
    quantity         integer NOT NULL CHECK (quantity <> 0),
    amount           numeric(10, 2) NOT NULL,
    identifiers      text[] NOT NULL DEFAULT '{}',
    occurred_at      timestamptz NOT NULL DEFAULT now(),
    recorded_by_id   bigint NOT NULL REFERENCES users(id)
);
CREATE INDEX idx_distribution_payments_distribution ON distribution_payments(distribution_id, occurred_at, id);
CREATE INDEX idx_distribution_payments_occurred_at ON distribution_payments(occurred_at, id);

-- What was already paid, as one entry per order at the time of its last payment (the
-- earlier ones weren't kept).
INSERT INTO distribution_payments (distribution_id, unit_id, beneficiary_id, description, quantity, amount, identifiers, occurred_at, recorded_by_id)
SELECT n.distribution_id, n.id, n.beneficiary_id, COALESCE(a.name, d.name), n.paid_quantity, n.price * n.paid_quantity,
       COALESCE((SELECT array_agg(i.identifier ORDER BY i.id) FROM distribution_unit_identifiers i WHERE i.unit_id = n.id AND i.paid_at IS NOT NULL), '{}'),
       n.paid_at, n.paid_marked_by_id
FROM distribution_units n
JOIN distributions d ON d.id = n.distribution_id
LEFT JOIN distribution_articles a ON a.id = n.article_id
WHERE n.paid_quantity > 0 AND n.paid_marked_by_id IS NOT NULL;

-- +goose Down
DROP TABLE distribution_payments;

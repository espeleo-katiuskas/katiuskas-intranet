-- +goose Up
-- Sacas come in the same kind of fixed set as ropes (migration 00013's
-- rope_type) — caving, canyoning, plus two purpose-based kinds (quick,
-- special) that don't map to a discipline. A separate enum from
-- rope_type, since bag allows two values rope doesn't.
CREATE TYPE bag_type AS ENUM ('caving', 'canyoning', 'quick', 'special');

ALTER TABLE inventory_items ADD COLUMN bag_type bag_type;

-- Any bag that already exists predates this column; caving is by far
-- the club's most common bag type, so it's the least-wrong backfill —
-- correct it by hand afterward for the few that aren't.
UPDATE inventory_items SET bag_type = 'caving' WHERE item_type = 'bag';

ALTER TABLE inventory_items
    ADD CONSTRAINT bag_requires_bag_type CHECK (
        item_type <> 'bag' OR bag_type IS NOT NULL
    ),
    ADD CONSTRAINT bag_type_only_for_bag CHECK (
        item_type = 'bag' OR bag_type IS NULL
    );

-- +goose Down
ALTER TABLE inventory_items
    DROP CONSTRAINT bag_type_only_for_bag,
    DROP CONSTRAINT bag_requires_bag_type;

ALTER TABLE inventory_items DROP COLUMN bag_type;

DROP TYPE bag_type;

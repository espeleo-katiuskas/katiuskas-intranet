-- +goose Up
-- Club gear tracked for loan, rental or sale. rope and bag are
-- individually tracked physical objects — their own code, always
-- exactly one unit — with type-specific attributes; gear is everything
-- else, a fungible stock item filed under an admin-managed category
-- (material_categories, the same pattern license_types uses: grows
-- with an INSERT instead of a code deploy).
CREATE TYPE item_material_type AS ENUM ('rope', 'bag', 'gear');
CREATE TYPE rope_type AS ENUM ('caving', 'canyoning');
CREATE TYPE bag_type AS ENUM ('caving', 'canyoning', 'quick', 'special');
CREATE TYPE item_status AS ENUM (
    'available', 'on_loan', 'rented', 'sold', 'lost', 'abandoned', 'maintenance', 'retired'
);

-- Every movement type either exchanges the item with a specific person
-- (loan and rental always name one, sale optionally does) or changes
-- its stock level with no recipient at all: acquisition brings new
-- units in, loss/abandonment/retirement permanently take units out
-- (lost in the field, left behind on a trip, or decommissioned).
CREATE TYPE movement_type AS ENUM (
    'loan', 'rental', 'sale', 'acquisition', 'loss', 'abandonment', 'retirement'
);
CREATE TYPE movement_status AS ENUM ('active', 'completed', 'overdue', 'cancelled');

CREATE TABLE material_categories (
    id   bigserial PRIMARY KEY,
    name text UNIQUE NOT NULL
);
INSERT INTO material_categories (name) VALUES
    ('Mosquetones'), ('Spits'), ('Placas'), ('Maillones'), ('Iluminación');

CREATE TABLE inventory_items (
    id                     bigserial PRIMARY KEY,
    item_type              item_material_type NOT NULL,
    -- Required for rope/bag (they identify a specific physical
    -- object); optional for gear, since some parts are generic or the
    -- brand simply isn't known.
    brand                  text,
    model                  text,
    code                   text UNIQUE,
    -- Required for gear (it may be the only thing that identifies an
    -- item with no known brand/model); doubles as "Observaciones" in
    -- the frontend for rope/bag, where it's optional.
    description            text,
    status                 item_status NOT NULL DEFAULT 'available',
    -- Loanable says whether this item can be loaned out at all;
    -- rentable/sellable need no such flag, since they're derived from
    -- whether a price is set, but a loan carries no price to derive
    -- that from.
    loanable               boolean NOT NULL DEFAULT true,
    rental_price_per_week  numeric(10,2),
    sale_price             numeric(10,2),
    -- Rope-only.
    rope_type              rope_type,
    diameter_mm            numeric(4,1),
    manufacture_year       integer,
    length_m               integer,
    -- Bag-only.
    bag_type               bag_type,
    capacity_liters        numeric(5,1),
    -- Gear-only.
    category_id            bigint REFERENCES material_categories(id),
    CONSTRAINT rope_requires_rope_fields CHECK (
        item_type <> 'rope' OR (rope_type IS NOT NULL AND diameter_mm IS NOT NULL
            AND manufacture_year IS NOT NULL AND length_m IS NOT NULL)
    ),
    CONSTRAINT rope_fields_only_for_rope CHECK (
        item_type = 'rope' OR (rope_type IS NULL AND diameter_mm IS NULL
            AND manufacture_year IS NULL AND length_m IS NULL)
    ),
    CONSTRAINT bag_requires_bag_type CHECK (item_type <> 'bag' OR bag_type IS NOT NULL),
    CONSTRAINT bag_type_only_for_bag CHECK (item_type = 'bag' OR bag_type IS NULL),
    CONSTRAINT bag_requires_capacity CHECK (item_type <> 'bag' OR capacity_liters IS NOT NULL),
    CONSTRAINT capacity_only_for_bag CHECK (item_type = 'bag' OR capacity_liters IS NULL),
    CONSTRAINT gear_requires_category CHECK (item_type <> 'gear' OR category_id IS NOT NULL),
    CONSTRAINT category_only_for_gear CHECK (item_type = 'gear' OR category_id IS NULL),
    CONSTRAINT gear_requires_description CHECK (item_type <> 'gear' OR description IS NOT NULL),
    CONSTRAINT rope_and_bag_require_brand_model CHECK (
        item_type = 'gear' OR (brand IS NOT NULL AND model IS NOT NULL)
    ),
    CONSTRAINT rope_and_bag_require_code CHECK (item_type = 'gear' OR code IS NOT NULL)
);

CREATE TABLE item_movements (
    id             bigserial PRIMARY KEY,
    item_id        bigint NOT NULL REFERENCES inventory_items(id),
    -- Nil for acquisition/loss/abandonment/retirement, which never
    -- have a recipient. Always set for loan/rental; optional for sale.
    user_id        bigint REFERENCES users(id),
    type           movement_type NOT NULL,
    quantity       integer NOT NULL DEFAULT 1,
    -- Defaults to today but can be backdated — whoever logs the
    -- movement may be doing so after the fact.
    transacted_at  date NOT NULL DEFAULT CURRENT_DATE,
    returned_at    date,
    -- Never given by the caller — always the item's own catalog price
    -- for that kind of movement, and always null except for
    -- rental/sale.
    price          numeric(10,2),
    status         movement_status NOT NULL DEFAULT 'active',
    recorded_by    bigint REFERENCES users(id),
    -- Required for loan and rental (always part of an activity);
    -- optional for everything else.
    activity_id    bigint REFERENCES activities(id),
    -- Free text, mainly useful on loss/abandonment/retirement to say
    -- why.
    notes          text,
    CONSTRAINT recipient_required_for_loan_and_rental CHECK (
        type NOT IN ('loan', 'rental') OR user_id IS NOT NULL
    ),
    CONSTRAINT recipient_only_for_loan_rental_sale CHECK (
        type IN ('loan', 'rental', 'sale') OR user_id IS NULL
    ),
    CONSTRAINT activity_required_for_loan_and_rental CHECK (
        type NOT IN ('loan', 'rental') OR activity_id IS NOT NULL
    ),
    CONSTRAINT price_required_for_rental_and_sale CHECK (
        type NOT IN ('rental', 'sale') OR price IS NOT NULL
    ),
    CONSTRAINT price_only_for_rental_and_sale CHECK (
        type IN ('rental', 'sale') OR price IS NULL
    ),
    CONSTRAINT returnable_only_for_loan_and_rental CHECK (
        type IN ('loan', 'rental') OR returned_at IS NULL
    )
);

CREATE INDEX idx_item_movements_item_id ON item_movements(item_id);
CREATE INDEX idx_item_movements_user_id ON item_movements(user_id);

-- total_quantity/available_quantity are never stored — they're always
-- computed from an item's own movement history, so they can never
-- drift from what actually happened. total_quantity is everything
-- ever acquired minus everything that left for good (sold, lost,
-- abandoned, retired); available_quantity is the same, further
-- reduced by whatever's currently out on an unreturned loan or rental.
CREATE VIEW inventory_item_quantities AS
SELECT
    i.id AS item_id,
    COALESCE(SUM(CASE
        WHEN m.type = 'acquisition' THEN m.quantity
        WHEN m.type IN ('sale', 'loss', 'abandonment', 'retirement') THEN -m.quantity
        ELSE 0
    END), 0) AS total_quantity,
    COALESCE(SUM(CASE
        WHEN m.type = 'acquisition' THEN m.quantity
        WHEN m.type IN ('sale', 'loss', 'abandonment', 'retirement') THEN -m.quantity
        WHEN m.type IN ('loan', 'rental') AND m.returned_at IS NULL THEN -m.quantity
        ELSE 0
    END), 0) AS available_quantity
FROM inventory_items i
LEFT JOIN item_movements m ON m.item_id = i.id
GROUP BY i.id;

-- +goose Down
DROP VIEW inventory_item_quantities;
DROP TABLE item_movements;
DROP TABLE inventory_items;
DROP TABLE material_categories;
DROP TYPE movement_status;
DROP TYPE movement_type;
DROP TYPE item_status;
DROP TYPE bag_type;
DROP TYPE rope_type;
DROP TYPE item_material_type;

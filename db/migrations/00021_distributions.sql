-- +goose Up
-- A distribution ("reparto") is one thing the club hands out to its members:
-- shirts, buffs, a rope, mugs, stickers. It may need choices (a size, a
-- model) and may have a price (0 = free), possibly a different one for extra
-- units. Members choose what they want, an admin/board reads off what has to
-- be ordered, and once it has arrived the people who may hand it out mark who
-- collected it. Each distribution is a single article: if two things have to
-- be handled separately, they are two distributions.

-- draft: being prepared, nobody sees it. open: members can choose.
-- closed: no more choices, what to order is known. ordered: the order has
-- been placed. received: it is at the club, ready to be collected. done:
-- everything has been collected (or the rest written off), nothing left to do.
CREATE TYPE distribution_status AS ENUM ('draft', 'open', 'closed', 'ordered', 'received', 'done');

-- all_members: everyone who is a member on the distribution's cutoff_date
-- can have it (and each still has to say which size they want). signup:
-- only those who sign up for it.
CREATE TYPE distribution_audience AS ENUM ('all_members', 'signup');

CREATE TABLE distributions (
    id           bigserial PRIMARY KEY,
    name         text NOT NULL,
    description  text,
    audience     distribution_audience NOT NULL DEFAULT 'all_members',
    status       distribution_status NOT NULL DEFAULT 'draft',
    -- Last moment to choose or change; NULL = no deadline beyond closing it by hand.
    closes_at    timestamptz,
    -- Only people who are members on this day can have it, so who is
    -- eligible doesn't shift as others join or leave while it is open.
    -- Required for all_members, where it also defines who it is for; for
    -- signup it is optional, and when set only those members can sign up.
    cutoff_date  date,
    CONSTRAINT cutoff_date_for_all_members CHECK (
        audience <> 'all_members' OR cutoff_date IS NOT NULL
    ),
    -- What the member's own unit costs; 0 when it is free.
    price        numeric(10, 2) NOT NULL DEFAULT 0 CHECK (price >= 0),
    -- What each extra unit costs, which may differ from price. NULL = there
    -- are no extras at all.
    extra_price  numeric(10, 2) CHECK (extra_price >= 0),
    -- How many extra units one person may have (counted by whom they are
    -- for, whoever registered them). NULL = no limit; only meaningful when
    -- extras exist.
    max_extra    integer CHECK (max_extra >= 0),
    CONSTRAINT max_extra_needs_extras CHECK (max_extra IS NULL OR extra_price IS NOT NULL),
    created_by   bigint NOT NULL REFERENCES users(id),
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- Something to choose, with the values it can take: "Talla" with
-- {S, M, L, XL}. A distribution has as many as it needs, or none.
CREATE TABLE distribution_options (
    id               bigserial PRIMARY KEY,
    distribution_id  bigint NOT NULL REFERENCES distributions(id) ON DELETE CASCADE,
    name             text NOT NULL,
    "values"         text[] NOT NULL CHECK (cardinality("values") >= 1),
    position         integer NOT NULL,
    UNIQUE (distribution_id, name)
);

-- One row per unit wanted, so two extra shirts can be two different sizes.
-- It is for beneficiary_id, who is who it belongs to; registered_by_id is
-- who actually made the entry, which is somebody else when a parent signs a
-- child up or a friend adds extras for another, and registered_at when. Both
-- are kept as they were, never overwritten by a later edit of the choices.
CREATE TABLE distribution_units (
    id                 bigserial PRIMARY KEY,
    distribution_id    bigint NOT NULL REFERENCES distributions(id) ON DELETE CASCADE,
    beneficiary_id     bigint NOT NULL REFERENCES users(id),
    registered_by_id   bigint NOT NULL REFERENCES users(id),
    registered_at      timestamptz NOT NULL DEFAULT now(),
    is_extra           boolean NOT NULL DEFAULT false,
    -- The values chosen, by option name: {"Talla": "M"}. Checked against the
    -- distribution's options by the application.
    choices            jsonb NOT NULL DEFAULT '{}',
    -- What it costs, fixed when it was registered: changing the distribution's
    -- price later doesn't change what somebody already agreed to.
    price              numeric(10, 2) NOT NULL CHECK (price >= 0),
    paid_at            timestamptz,
    paid_marked_by_id  bigint REFERENCES users(id),
    picked_up_at       timestamptz,
    -- Who wrote the collection down (admin, board or material)...
    pickup_recorded_by_id  bigint REFERENCES users(id),
    -- ...and who it was handed to: any member, not necessarily the
    -- beneficiary, when somebody collects things for others.
    picked_up_by_id    bigint REFERENCES users(id),
    CONSTRAINT paid_fields_together CHECK ((paid_at IS NULL) = (paid_marked_by_id IS NULL)),
    CONSTRAINT picked_up_fields_together CHECK (
        (picked_up_at IS NULL) = (pickup_recorded_by_id IS NULL)
        AND (picked_up_at IS NULL) = (picked_up_by_id IS NULL)
    )
);

-- At most one own (non-extra) unit per person.
CREATE UNIQUE INDEX idx_distribution_units_own
    ON distribution_units(distribution_id, beneficiary_id) WHERE NOT is_extra;
CREATE INDEX idx_distribution_units_beneficiary_id ON distribution_units(beneficiary_id);

-- +goose Down
DROP TABLE distribution_units;
DROP TABLE distribution_options;
DROP TABLE distributions;
DROP TYPE distribution_audience;
DROP TYPE distribution_status;

-- +goose Up
-- A distribution can hand out several articles that members ask for in any
-- quantity, each with its own price and an optional limit on how many exist in
-- all (the Christmas lottery: 200 "papeletas" at 5 € and 1000 "décimos" at
-- 20 €, as many as each member wants until they run out). A distribution with
-- no articles keeps working as before: one article described by the
-- distribution itself, one own unit per person plus extras.
CREATE TABLE distribution_articles (
    id               bigserial PRIMARY KEY,
    distribution_id  bigint NOT NULL REFERENCES distributions(id) ON DELETE CASCADE,
    name             text NOT NULL,
    -- What one unit of it costs.
    price            numeric(10, 2) NOT NULL DEFAULT 0 CHECK (price >= 0),
    -- How many units there are to give out in all, whoever asks; NULL = no limit.
    max_total        integer CHECK (max_total >= 1),
    -- How many of it one person may ask for; NULL = no cap (max_total is the
    -- cap in all).
    max_per_person   integer CHECK (max_per_person >= 1),
    -- Each unit has its own number — a lottery "papeleta" its serial number, a
    -- "décimo" its series and fraction — which nobody gives when asking but is
    -- written down when the units are handed out: a row of quantity N then needs
    -- N identifiers before it can be collected.
    has_identifiers  boolean NOT NULL DEFAULT false,
    position         integer NOT NULL,
    UNIQUE (distribution_id, name)
);

-- A unit of an article is one row per person and article with a quantity, not
-- one row per thing: nobody wants to mark 20 décimos one by one as collected.
-- price stays what ONE unit cost when it was entered, so the row costs
-- price * quantity. The rows of a distribution without articles have no
-- article and a quantity of 1, as ever.
ALTER TABLE distribution_units
    ADD COLUMN article_id bigint REFERENCES distribution_articles(id) ON DELETE CASCADE,
    ADD COLUMN quantity integer NOT NULL DEFAULT 1 CHECK (quantity >= 1),
    ADD CONSTRAINT quantity_only_for_articles CHECK (article_id IS NOT NULL OR quantity = 1),
    ADD CONSTRAINT article_units_are_plain CHECK (article_id IS NULL OR (NOT is_extra AND choices = '{}'));

-- The one-own-unit rule is about distributions without articles; with them a
-- person has one row per article.
DROP INDEX idx_distribution_units_own;
CREATE UNIQUE INDEX idx_distribution_units_own
    ON distribution_units(distribution_id, beneficiary_id) WHERE NOT is_extra AND article_id IS NULL;
CREATE UNIQUE INDEX idx_distribution_units_article
    ON distribution_units(article_id, beneficiary_id) WHERE article_id IS NOT NULL;

-- The identifiers of a unit's items, unique within the article: the same
-- papeleta can't be in two people's hands.
CREATE TABLE distribution_unit_identifiers (
    id          bigserial PRIMARY KEY,
    unit_id     bigint NOT NULL REFERENCES distribution_units(id) ON DELETE CASCADE,
    article_id  bigint NOT NULL REFERENCES distribution_articles(id) ON DELETE CASCADE,
    identifier  text NOT NULL CHECK (identifier <> ''),
    UNIQUE (article_id, identifier)
);
CREATE INDEX idx_distribution_unit_identifiers_unit_id ON distribution_unit_identifiers(unit_id);

-- +goose Down
DROP TABLE distribution_unit_identifiers;
DELETE FROM distribution_units WHERE article_id IS NOT NULL;
DROP INDEX idx_distribution_units_article;
DROP INDEX idx_distribution_units_own;
CREATE UNIQUE INDEX idx_distribution_units_own
    ON distribution_units(distribution_id, beneficiary_id) WHERE NOT is_extra;
ALTER TABLE distribution_units
    DROP CONSTRAINT article_units_are_plain,
    DROP CONSTRAINT quantity_only_for_articles,
    DROP COLUMN quantity,
    DROP COLUMN article_id;
DROP TABLE distribution_articles;

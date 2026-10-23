-- +goose Up
-- The numbers of an article's items can be described instead of typed one by
-- one: a list of fields ("Serie", "Fracción"), each with the values it can take
-- written as a spec ("124-133, 137"). Every combination is an item that exists
-- (the lottery: papeletas 0001-0200; décimos series 124-133 times fractions
-- 1-10), so what is free and who has what can be told, and the next free ones
-- can be given out automatically. Empty = free-text numbers, as before.
-- [{"name": "Serie", "spec": "124-133, 137"}, ...]
ALTER TABLE distribution_articles
    ADD COLUMN identifier_fields jsonb NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE distribution_articles DROP COLUMN identifier_fields;

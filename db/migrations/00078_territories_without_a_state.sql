-- +goose Up
-- The three entries of ISO 3166-1 that are not states of the United Nations and that no state
-- administers undisputed: Antarctica (AQ), Western Sahara (EH) and Taiwan (TW). They are zones with
-- no zone above them, like a country, but their kind is "Territorio" (as for the territories of
-- migration 00077) and not "País", which would say they are states: ISO itself calls its entries
-- "countries or areas", and this takes no side on what is disputed. Their code is the ISO one, so
-- that the geocoder of OpenStreetMap, which names them by it, finds them.
INSERT INTO areas (parent_id, kind, name, norm_name, code) VALUES
    (NULL, 'Territorio', 'Antártida', 'antartida', 'AQ'),
    (NULL, 'Territorio', 'Sáhara Occidental', 'sahara occidental', 'EH'),
    (NULL, 'Territorio', 'Taiwán', 'taiwan', 'TW');

-- +goose Down
DELETE FROM areas WHERE parent_id IS NULL AND code IN ('AQ', 'EH', 'TW');

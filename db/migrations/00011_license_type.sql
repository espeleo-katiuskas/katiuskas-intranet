-- +goose Up
-- Zone and modality turned out not to be independent dimensions worth
-- splitting apart: real license types don't decompose cleanly ("FME",
-- "Murciana", "A BASICA Infantil"), so a license is just one free-text
-- type string, not a zone plus a modality.
ALTER TABLE licenses ADD COLUMN type text;
UPDATE licenses SET type = zone || ' ' || modality WHERE type IS NULL;
ALTER TABLE licenses ALTER COLUMN type SET NOT NULL;
ALTER TABLE licenses DROP COLUMN zone;
ALTER TABLE licenses DROP COLUMN modality;

-- +goose Down
-- Lossy: an arbitrary type string ("FME", "A BASICA Infantil") doesn't
-- decompose back into zone/modality the way "A BASICA" happens to.
ALTER TABLE licenses ADD COLUMN zone text;
ALTER TABLE licenses ADD COLUMN modality text;
UPDATE licenses SET
    zone = split_part(type, ' ', 1),
    modality = substring(type from position(' ' in type) + 1);
ALTER TABLE licenses ALTER COLUMN zone SET NOT NULL;
ALTER TABLE licenses ALTER COLUMN modality SET NOT NULL;
ALTER TABLE licenses DROP COLUMN type;

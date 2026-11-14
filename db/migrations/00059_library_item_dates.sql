-- +goose Up
-- When the club got each item of the library and, if it left for good, when and why: lost, withdrawn
-- (taken out of the library) or sold. An item that has left is no longer available. The items that
-- already exist count as acquired on the day they were added (the club's day).
ALTER TABLE library_items
    ADD COLUMN acquired_on date NOT NULL DEFAULT CURRENT_DATE,
    ADD COLUMN ended_on    date,
    ADD COLUMN end_reason  text CHECK (end_reason IN ('lost', 'withdrawn', 'sold'));

UPDATE library_items SET acquired_on = (created_at AT TIME ZONE 'Europe/Madrid')::date;

ALTER TABLE library_items
    ADD CONSTRAINT library_items_end_complete CHECK ((ended_on IS NULL) = (end_reason IS NULL)),
    ADD CONSTRAINT library_items_end_after_start CHECK (ended_on IS NULL OR ended_on >= acquired_on);

-- +goose Down
ALTER TABLE library_items
    DROP CONSTRAINT library_items_end_after_start,
    DROP CONSTRAINT library_items_end_complete,
    DROP COLUMN end_reason,
    DROP COLUMN ended_on,
    DROP COLUMN acquired_on;

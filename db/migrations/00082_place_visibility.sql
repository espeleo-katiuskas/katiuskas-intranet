-- +goose Up
-- Who may see a place, in three levels, each for the club alone ('club') or for anybody ('public'):
--   place_audience     the place itself: that it exists, with its name, type, state, zone and measures.
--                      Where it is closed, whoever may not see it does not know it exists.
--   location_audience  where it is: the main coordinate, the other points and how to get there.
--   content_audience   what is said and shown of it: the description, the technical sheet, the files.
-- Who wrote it and the activities that went there are never public. Everything is for the club until an
-- admin or a board member opens it. A level cannot be more open than the place, and a place that is
-- protected (bats, archaeology, private land...) cannot have anything public.
ALTER TABLE catalogue.places
    ADD COLUMN place_audience    text    NOT NULL DEFAULT 'club',
    ADD COLUMN location_audience text    NOT NULL DEFAULT 'club',
    ADD COLUMN content_audience  text    NOT NULL DEFAULT 'club',
    ADD COLUMN protected         boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT places_audiences_known CHECK (
        place_audience IN ('club', 'public') AND location_audience IN ('club', 'public')
        AND content_audience IN ('club', 'public')),
    ADD CONSTRAINT places_location_not_more_open_than_place CHECK (
        location_audience = 'club' OR place_audience = 'public'),
    ADD CONSTRAINT places_content_not_more_open_than_place CHECK (
        content_audience = 'club' OR place_audience = 'public'),
    ADD CONSTRAINT places_protected_is_closed CHECK (
        NOT protected OR (place_audience = 'club' AND location_audience = 'club' AND content_audience = 'club'));

-- +goose Down
ALTER TABLE catalogue.places
    DROP CONSTRAINT places_protected_is_closed,
    DROP CONSTRAINT places_content_not_more_open_than_place,
    DROP CONSTRAINT places_location_not_more_open_than_place,
    DROP CONSTRAINT places_audiences_known,
    DROP COLUMN protected,
    DROP COLUMN content_audience,
    DROP COLUMN location_audience,
    DROP COLUMN place_audience;

-- +goose Up
-- Where an activity took place, or is to: the places (caves, canyons…) of the catalogue it goes to. An
-- activity may go to several (a weekend in two caves) and a place has had many activities, so it is a
-- plain join table. The links are the activity's: deleting it takes them with it, but a place that has
-- activities is not deleted (the history stays), which `ON DELETE RESTRICT` backs up.
CREATE TABLE activity_places (
    activity_id bigint NOT NULL REFERENCES activities(id) ON DELETE RESTRICT,
    place_id    bigint NOT NULL REFERENCES places(id) ON DELETE RESTRICT,
    PRIMARY KEY (activity_id, place_id)
);
CREATE INDEX idx_activity_places_place_id ON activity_places(place_id);

-- +goose Down
DROP TABLE activity_places;

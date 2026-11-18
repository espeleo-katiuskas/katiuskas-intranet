-- +goose Up
-- Who added a zone through the API. It is empty for the zones that the seeds load (the countries,
-- Spain's communities, provinces and municipalities, Ceuta and Melilla), and for those that were
-- added before this column existed, since nobody recorded who did it.
ALTER TABLE areas ADD COLUMN created_by bigint REFERENCES users(id) ON DELETE RESTRICT;

-- +goose Down
ALTER TABLE areas DROP COLUMN created_by;

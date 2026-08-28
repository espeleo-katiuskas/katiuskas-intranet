-- +goose Up
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- +goose Down
DROP EXTENSION IF EXISTS btree_gist;

-- +goose Up
ALTER TABLE users ADD COLUMN postal_code text;
ALTER TABLE users ADD COLUMN city text;
ALTER TABLE users ADD COLUMN province text;

-- +goose Down
ALTER TABLE users DROP COLUMN postal_code;
ALTER TABLE users DROP COLUMN city;
ALTER TABLE users DROP COLUMN province;

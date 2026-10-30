-- +goose Up
-- Who can see, and act on, a distribution at all: everybody (the default, as it always was), or
-- only material, board and admin; board and admin; or admin alone. Each level includes the ones
-- above it. Separate from the status: a draft is only for admin and board whatever this says.
CREATE TYPE distribution_visibility AS ENUM ('all', 'material', 'board', 'admin');

ALTER TABLE distributions
    ADD COLUMN visibility distribution_visibility NOT NULL DEFAULT 'all';

-- +goose Down
ALTER TABLE distributions DROP COLUMN visibility;
DROP TYPE distribution_visibility;

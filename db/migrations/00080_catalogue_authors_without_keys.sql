-- +goose Up
-- The catalogue (places, systems, their files and the zones) no longer depends on how the club stores
-- its people: who created, changed or attached something is a plain reference to a person, with no
-- foreign key to users. What the key did, keep a person with such records from being deleted, is now
-- done by users.Service.Delete, which asks the catalogue (users.AuthorGuard).
ALTER TABLE places DROP CONSTRAINT places_created_by_fkey;
ALTER TABLE places DROP CONSTRAINT places_updated_by_fkey;
ALTER TABLE place_systems DROP CONSTRAINT place_systems_created_by_fkey;
ALTER TABLE place_systems DROP CONSTRAINT place_systems_updated_by_fkey;
ALTER TABLE place_files DROP CONSTRAINT place_files_uploaded_by_fkey;
ALTER TABLE areas DROP CONSTRAINT areas_created_by_fkey;

-- +goose Down
ALTER TABLE areas ADD CONSTRAINT areas_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE RESTRICT;
ALTER TABLE place_files ADD CONSTRAINT place_files_uploaded_by_fkey
    FOREIGN KEY (uploaded_by) REFERENCES users(id) ON DELETE RESTRICT;
ALTER TABLE place_systems ADD CONSTRAINT place_systems_updated_by_fkey
    FOREIGN KEY (updated_by) REFERENCES users(id) ON DELETE RESTRICT;
ALTER TABLE place_systems ADD CONSTRAINT place_systems_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE RESTRICT;
ALTER TABLE places ADD CONSTRAINT places_updated_by_fkey
    FOREIGN KEY (updated_by) REFERENCES users(id) ON DELETE RESTRICT;
ALTER TABLE places ADD CONSTRAINT places_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE RESTRICT;

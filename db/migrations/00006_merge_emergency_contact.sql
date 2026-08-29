-- +goose Up
ALTER TABLE users ADD COLUMN emergency_contact text;

UPDATE users
SET emergency_contact = trim(both ' · ' from concat_ws(' · ', emergency_contact_name, emergency_contact_phone))
WHERE emergency_contact_name IS NOT NULL OR emergency_contact_phone IS NOT NULL;

ALTER TABLE users DROP COLUMN emergency_contact_name;
ALTER TABLE users DROP COLUMN emergency_contact_phone;

-- +goose Down
-- Lossy: a free-text contact can't be reliably split back into name/phone.
ALTER TABLE users ADD COLUMN emergency_contact_name text;
ALTER TABLE users ADD COLUMN emergency_contact_phone text;
ALTER TABLE users DROP COLUMN emergency_contact;

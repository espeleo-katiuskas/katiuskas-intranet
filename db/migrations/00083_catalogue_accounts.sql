-- +goose Up
-- The catalogue has accounts of its own, which have nothing to do with the intranet's table of people.
-- An account is whoever or whatever acts on the catalogue: a person (what is shown as the author of a
-- change) or a service (a client of the catalogue, of which the intranet of the club is the first).
-- Who created, changed or attached something is the id of an account, a real foreign key with a
-- meaning inside the catalogue, so that it can leave the intranet and take its authors with it.
--
-- An issuer is a service that vouches for the people it sends (the intranet says "this is the person
-- whose subject is X"), and an identity is how an issuer knows a person: the account of the person is
-- made the first time they act. The subject is an identifier the issuer gives to each of its people
-- (users.subject, a random uuid that never changes and says nothing of how the intranet stores them);
-- it lives here, in the identities, and in no other column of the catalogue.
ALTER TABLE users ADD COLUMN subject uuid NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE users ADD CONSTRAINT users_subject_key UNIQUE (subject);

CREATE TABLE catalogue.accounts (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind         text        NOT NULL CHECK (kind IN ('person', 'service')),
    display_name text        NOT NULL CHECK (btrim(display_name) <> ''),
    created_at   timestamptz NOT NULL DEFAULT now(),
    -- Only while this migration runs: the person of the intranet an account is made for.
    migrated_user_id bigint
);

CREATE TABLE catalogue.issuers (
    issuer     text   PRIMARY KEY CHECK (btrim(issuer) <> ''),
    account_id bigint NOT NULL UNIQUE REFERENCES catalogue.accounts (id) ON DELETE RESTRICT
);

CREATE TABLE catalogue.account_identities (
    issuer     text   NOT NULL REFERENCES catalogue.issuers (issuer) ON DELETE RESTRICT,
    subject    text   NOT NULL CHECK (btrim(subject) <> ''),
    account_id bigint NOT NULL REFERENCES catalogue.accounts (id) ON DELETE RESTRICT,
    PRIMARY KEY (issuer, subject)
);
CREATE INDEX idx_account_identities_account_id ON catalogue.account_identities (account_id);

-- The intranet of the club, the first client.
INSERT INTO catalogue.accounts (kind, display_name) VALUES ('service', 'Club Katiuskas intranet');
INSERT INTO catalogue.issuers (issuer, account_id)
    SELECT 'katiuskas-intranet', id FROM catalogue.accounts WHERE kind = 'service';

-- An account for each person who has made something in the catalogue, named as the intranet names them
-- (their login), and the identity that links it to them.
INSERT INTO catalogue.accounts (kind, display_name, migrated_user_id)
    SELECT 'person', u.login, u.id FROM users u WHERE u.id IN (
        SELECT created_by FROM catalogue.places UNION SELECT updated_by FROM catalogue.places
        UNION SELECT created_by FROM catalogue.place_systems UNION SELECT updated_by FROM catalogue.place_systems
        UNION SELECT uploaded_by FROM catalogue.place_files
        UNION SELECT created_by FROM catalogue.areas WHERE created_by IS NOT NULL);
INSERT INTO catalogue.account_identities (issuer, subject, account_id)
    SELECT 'katiuskas-intranet', u.subject::text, a.id
    FROM catalogue.accounts a JOIN users u ON u.id = a.migrated_user_id;

-- The columns that held ids of users hold ids of accounts, with a foreign key.
UPDATE catalogue.places p SET created_by = c.id, updated_by = u.id
    FROM catalogue.accounts c, catalogue.accounts u
    WHERE c.migrated_user_id = p.created_by AND u.migrated_user_id = p.updated_by;
UPDATE catalogue.place_systems s SET created_by = c.id, updated_by = u.id
    FROM catalogue.accounts c, catalogue.accounts u
    WHERE c.migrated_user_id = s.created_by AND u.migrated_user_id = s.updated_by;
UPDATE catalogue.place_files f SET uploaded_by = a.id FROM catalogue.accounts a WHERE a.migrated_user_id = f.uploaded_by;
UPDATE catalogue.areas z SET created_by = a.id FROM catalogue.accounts a WHERE a.migrated_user_id = z.created_by;
ALTER TABLE catalogue.places
    ADD CONSTRAINT places_created_by_fkey FOREIGN KEY (created_by) REFERENCES catalogue.accounts (id) ON DELETE RESTRICT,
    ADD CONSTRAINT places_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES catalogue.accounts (id) ON DELETE RESTRICT;
ALTER TABLE catalogue.place_systems
    ADD CONSTRAINT place_systems_created_by_fkey FOREIGN KEY (created_by) REFERENCES catalogue.accounts (id) ON DELETE RESTRICT,
    ADD CONSTRAINT place_systems_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES catalogue.accounts (id) ON DELETE RESTRICT;
ALTER TABLE catalogue.place_files
    ADD CONSTRAINT place_files_uploaded_by_fkey FOREIGN KEY (uploaded_by) REFERENCES catalogue.accounts (id) ON DELETE RESTRICT;
ALTER TABLE catalogue.areas
    ADD CONSTRAINT areas_created_by_fkey FOREIGN KEY (created_by) REFERENCES catalogue.accounts (id) ON DELETE RESTRICT;
ALTER TABLE catalogue.accounts DROP COLUMN migrated_user_id;

-- +goose Down
ALTER TABLE catalogue.areas DROP CONSTRAINT areas_created_by_fkey;
ALTER TABLE catalogue.place_files DROP CONSTRAINT place_files_uploaded_by_fkey;
ALTER TABLE catalogue.place_systems DROP CONSTRAINT place_systems_updated_by_fkey, DROP CONSTRAINT place_systems_created_by_fkey;
ALTER TABLE catalogue.places DROP CONSTRAINT places_updated_by_fkey, DROP CONSTRAINT places_created_by_fkey;

-- Back to the ids of the people of the intranet, through the identity. An account that is not one of
-- them (a person of another issuer) cannot go back, and stops the migration.
ALTER TABLE catalogue.accounts ADD COLUMN migrated_user_id bigint;
UPDATE catalogue.accounts a SET migrated_user_id = u.id
    FROM catalogue.account_identities i JOIN users u ON u.subject::text = i.subject
    WHERE i.account_id = a.id AND i.issuer = 'katiuskas-intranet';
UPDATE catalogue.places p SET created_by = c.migrated_user_id, updated_by = u.migrated_user_id
    FROM catalogue.accounts c, catalogue.accounts u WHERE c.id = p.created_by AND u.id = p.updated_by;
UPDATE catalogue.place_systems s SET created_by = c.migrated_user_id, updated_by = u.migrated_user_id
    FROM catalogue.accounts c, catalogue.accounts u WHERE c.id = s.created_by AND u.id = s.updated_by;
UPDATE catalogue.place_files f SET uploaded_by = a.migrated_user_id FROM catalogue.accounts a WHERE a.id = f.uploaded_by;
UPDATE catalogue.areas z SET created_by = a.migrated_user_id FROM catalogue.accounts a WHERE a.id = z.created_by;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM catalogue.places p WHERE NOT EXISTS (SELECT 1 FROM users WHERE id = p.created_by)
                   OR NOT EXISTS (SELECT 1 FROM users WHERE id = p.updated_by))
        OR EXISTS (SELECT 1 FROM catalogue.place_systems s WHERE NOT EXISTS (SELECT 1 FROM users WHERE id = s.created_by)
                   OR NOT EXISTS (SELECT 1 FROM users WHERE id = s.updated_by))
        OR EXISTS (SELECT 1 FROM catalogue.place_files f WHERE NOT EXISTS (SELECT 1 FROM users WHERE id = f.uploaded_by))
        OR EXISTS (SELECT 1 FROM catalogue.areas z WHERE z.created_by IS NOT NULL
                   AND NOT EXISTS (SELECT 1 FROM users WHERE id = z.created_by)) THEN
        RAISE EXCEPTION 'an author of the catalogue is not a person of the intranet: it cannot go back';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE catalogue.accounts DROP COLUMN migrated_user_id;

DROP TABLE catalogue.account_identities;
DROP TABLE catalogue.issuers;
DROP TABLE catalogue.accounts;
ALTER TABLE users DROP CONSTRAINT users_subject_key;
ALTER TABLE users DROP COLUMN subject;

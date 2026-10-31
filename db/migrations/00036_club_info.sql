-- +goose Up
-- The "datos de interés" shown on the home page (the club's name, tax id, address, bank account,
-- the Bizum number...): a list of labelled values an administrator edits from /admin, so that
-- they are not fixed in the frontend, and so that one that should not be public (the Bizum
-- number) does not have to be written in the repository. An item may also offer a button that
-- copies something (the tax id without its dash, the IBAN without spaces).
CREATE TABLE club_info_items (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    position   integer NOT NULL,
    label      text NOT NULL CHECK (btrim(label) <> ''),
    value      text NOT NULL CHECK (btrim(value) <> ''),
    copy_value text CHECK (copy_value IS NULL OR btrim(copy_value) <> ''),
    copy_label text CHECK (copy_label IS NULL OR btrim(copy_label) <> ''),
    CONSTRAINT club_info_copy_label_needs_value CHECK (copy_label IS NULL OR copy_value IS NOT NULL)
);

-- Nothing is seeded: the data is entered from /admin, so none of it is written in the repository.

-- +goose Down
DROP TABLE club_info_items;

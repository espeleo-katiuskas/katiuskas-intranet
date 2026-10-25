-- +goose Up
-- The hierarchy of accounts only groups: an account either has child accounts or
-- has transactions, never both (a "Bancos" account holds one per bank, which hold
-- one per account in it, and only those last ones move money). Both directions are
-- refused here, in the database, so no path to the data — the API, the
-- transactions the payments of a distribution make — can skip it: an entry can't
-- be posted to an account that has children, and an account can't be given as a
-- parent (made, or moved under it) when it already has entries.
-- +goose StatementBegin
CREATE FUNCTION check_entry_account_is_leaf() RETURNS trigger AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM accounts WHERE parent_id = NEW.account_id) THEN
        RAISE EXCEPTION 'account % has child accounts, so it can not have transactions', NEW.account_id
            USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'account_has_children';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER transaction_entries_account_is_leaf
    BEFORE INSERT OR UPDATE OF account_id ON transaction_entries
    FOR EACH ROW EXECUTE FUNCTION check_entry_account_is_leaf();

-- +goose StatementBegin
CREATE FUNCTION check_parent_account_has_no_entries() RETURNS trigger AS $$
BEGIN
    IF NEW.parent_id IS NOT NULL AND EXISTS (SELECT 1 FROM transaction_entries WHERE account_id = NEW.parent_id) THEN
        RAISE EXCEPTION 'account % has transactions, so it can not have child accounts', NEW.parent_id
            USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'parent_has_entries';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER accounts_parent_has_no_entries
    BEFORE INSERT OR UPDATE OF parent_id ON accounts
    FOR EACH ROW EXECUTE FUNCTION check_parent_account_has_no_entries();

-- +goose Down
DROP TRIGGER accounts_parent_has_no_entries ON accounts;
DROP FUNCTION check_parent_account_has_no_entries();
DROP TRIGGER transaction_entries_account_is_leaf ON transaction_entries;
DROP FUNCTION check_entry_account_is_leaf();

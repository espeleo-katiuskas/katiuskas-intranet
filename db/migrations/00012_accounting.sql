-- +goose Up
-- The club's own chart of accounts: a name/code pair, optionally nested
-- under a broader account (parent_id) — e.g. "570 Caja" and "572 Banco"
-- both under "57 Tesorería". A leaf account is where a transaction
-- entry actually posts; a parent account exists purely to group its
-- children under a broader label for reporting, though nothing here
-- stops an entry from posting directly to a non-leaf account too.
CREATE TABLE accounts (
    id         bigserial PRIMARY KEY,
    name       text NOT NULL,
    code       text UNIQUE NOT NULL,
    parent_id  bigint REFERENCES accounts(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    -- Manual display order among sibling accounts (same parent_id,
    -- including NULL together as one top-level group) — see
    -- accounting.Repository.MoveAccount. Not unique on purpose: a gap
    -- or a rare duplicate (nothing else in this schema enforces one)
    -- is harmless, since it's only ever used to ORDER BY, never to
    -- identify a row.
    position   integer NOT NULL
);
CREATE INDEX idx_accounts_parent_id_position ON accounts(parent_id, position);

-- One real movement of money, described once and dated (occurred_at is
-- a full timestamp, not just a calendar date — unlike, say,
-- memberships.start_date — since when during the day a transaction
-- happened is meaningful here). What actually moved is entirely in
-- transaction_entries below; this row alone says nothing about amounts.
CREATE TABLE transactions (
    id          bigserial PRIMARY KEY,
    description text NOT NULL,
    occurred_at timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_transactions_occurred_at ON transactions(occurred_at);

-- Standard double-entry bookkeeping: every transaction is split across
-- two or more entries, one per account it touches, each moving amount
-- into (positive) or out of (negative) that account — so a transaction
-- always says both where money came from and where it went, never just
-- one side of it. A zero amount is meaningless (a no-op leg) and
-- rejected outright; "at least two entries" and "entries sum to zero"
-- can't be expressed as a plain CHECK (they depend on every sibling
-- row, not just this one), so they're enforced by the deferred
-- constraint trigger below instead — the same "lean on Postgres when it
-- can express the invariant" this schema already uses elsewhere (see
-- board_members' own EXCLUDE constraint, or inventory_items' type-
-- specific CHECKs).
CREATE TABLE transaction_entries (
    id             bigserial PRIMARY KEY,
    transaction_id bigint NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    -- Deliberately no ON DELETE here (the default, RESTRICT): deleting
    -- an account that already has posted entries would silently orphan
    -- financial history instead of just failing loudly.
    account_id     bigint NOT NULL REFERENCES accounts(id),
    amount         numeric(10,2) NOT NULL,
    CONSTRAINT amount_not_zero CHECK (amount <> 0)
);
CREATE INDEX idx_transaction_entries_transaction_id ON transaction_entries(transaction_id);
CREATE INDEX idx_transaction_entries_account_id ON transaction_entries(account_id);

-- Fires once per entry row written (inserted, updated or deleted), but
-- DEFERRABLE INITIALLY DEFERRED means every queued firing actually runs
-- at COMMIT — so writing a transaction's several entries one at a time
-- (see accounting.Repository.CreateTransaction/UpdateTransaction, both
-- of which do exactly this inside their own DB transaction) is only
-- ever validated once, against the final set, not against each
-- intermediate half-written state.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION check_transaction_balance() RETURNS trigger AS $$
DECLARE
    affected_transaction_id bigint := COALESCE(NEW.transaction_id, OLD.transaction_id);
    entry_count integer;
    total numeric;
BEGIN
    -- Deleting a whole transaction cascades to every one of its
    -- entries, each of which fires this same trigger — by commit time
    -- the transaction row itself is gone too, and there's nothing left
    -- to balance, so skip out rather than wrongly rejecting a
    -- transaction's own valid, complete deletion for having "too few"
    -- (zero) remaining entries.
    IF NOT EXISTS (SELECT 1 FROM transactions WHERE id = affected_transaction_id) THEN
        RETURN NULL;
    END IF;

    SELECT COUNT(*), COALESCE(SUM(amount), 0) INTO entry_count, total
    FROM transaction_entries WHERE transaction_id = affected_transaction_id;

    IF entry_count < 2 THEN
        RAISE EXCEPTION 'transaction % must have at least two entries (has %)', affected_transaction_id, entry_count
            USING ERRCODE = 'check_violation';
    END IF;
    IF total <> 0 THEN
        RAISE EXCEPTION 'transaction % entries do not sum to zero (got %)', affected_transaction_id, total
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER transaction_entries_balance
    AFTER INSERT OR UPDATE OR DELETE ON transaction_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION check_transaction_balance();

-- Not a system role (see roles.is_system) — an organizational label an
-- admin assigns to whoever manages the books, the same non-system
-- "material" role already does for inventory movements. admin can
-- always write here regardless; this is for someone who isn't an admin
-- but should still be able to (see accounting.Handler.RegisterRoutes).
INSERT INTO roles (name, description, is_system, is_automatic) VALUES
    ('money', 'Gestiona la contabilidad: cuentas y movimientos de dinero.', false, false);

-- +goose Down
DELETE FROM roles WHERE name = 'money';
DROP TRIGGER transaction_entries_balance ON transaction_entries;
DROP FUNCTION check_transaction_balance();
DROP TABLE transaction_entries;
DROP TABLE transactions;
DROP TABLE accounts;

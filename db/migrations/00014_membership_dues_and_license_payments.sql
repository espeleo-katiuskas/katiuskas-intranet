-- +goose Up
-- Every year a socio remains a member, they owe a cuota to cover it —
-- the first one (alongside the one-time matrícula) the same day they
-- join, then once more each following year for as long as they stay a
-- socio. One row per (person, calendar year) covered, always tied to
-- the transaction that actually recorded the payment — a due with no
-- transaction behind it doesn't mean anything yet, so there's no
-- "unpaid" row to represent, unlike accounts/transactions themselves.
CREATE TABLE membership_dues (
    id             bigserial PRIMARY KEY,
    user_id        bigint NOT NULL REFERENCES users(id),
    year           integer NOT NULL,
    transaction_id bigint NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    created_at     timestamptz NOT NULL DEFAULT now(),
    -- One payment per person per year — never two.
    UNIQUE (user_id, year)
);
CREATE INDEX idx_membership_dues_user_id ON membership_dues(user_id);

-- A federative license also has to be paid for, so it gets the same
-- transaction_id treatment — but unlike a due, a license is a real,
-- independently meaningful record on its own (proof of federation)
-- regardless of its payment, so deleting the transaction that paid for
-- it doesn't delete the license itself, just the linkage (RESTRICT,
-- the default, actually: a transaction that's the recorded payment for
-- a real license can't be deleted out from under it at all — the
-- license would have to be reassigned or removed first). year is its
-- own column, separate from issued_date: a license can be requested
-- (and issued_date-stamped) in December for the *following* year, so
-- the two aren't always the same calendar year the way they used to be
-- assumed to be.
ALTER TABLE licenses ADD COLUMN year integer NOT NULL;
ALTER TABLE licenses ADD COLUMN transaction_id bigint NOT NULL REFERENCES transactions(id);

-- +goose Down
ALTER TABLE licenses DROP COLUMN transaction_id;
ALTER TABLE licenses DROP COLUMN year;
DROP TABLE membership_dues;

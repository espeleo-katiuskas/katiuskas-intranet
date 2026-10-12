-- +goose Up
-- Which accounts a transaction has to be posted to for it to be allowed to
-- record membership dues (cuotas anuales) or federative licenses. Each is
-- its own flag: the club may collect the two into different accounts, and
-- an account for one has no business being offered for the other. Both
-- default to false, so nothing can be recorded against a transaction
-- until an admin or the "money" role marks the accounts that actually
-- receive those payments; dues and licenses already recorded are left as
-- they are, since the rule is only checked when a new one is registered
-- (memberships.Service.RecordDue, licenses.Service.Create).
ALTER TABLE accounts ADD COLUMN allows_dues boolean NOT NULL DEFAULT false;
ALTER TABLE accounts ADD COLUMN allows_licenses boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE accounts DROP COLUMN allows_licenses;
ALTER TABLE accounts DROP COLUMN allows_dues;

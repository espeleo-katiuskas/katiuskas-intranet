-- +goose Up
-- The yearly budget: one per calendar year, in two parts (income and expenses), each made of
-- line items with an estimated amount. A line item stands for one or more accounts of the
-- matching type, and an account belongs to at most one line item per year. That every income or
-- expense account is covered, that an item only has accounts of its part's type, and that the
-- two parts add up to the same amount are rules the API checks when it saves the whole budget
-- (they span rows, and an account's type can change later); the database keeps what a row can say
-- by itself.
CREATE TYPE budget_kind AS ENUM ('income', 'expense');

CREATE TABLE budgets (
    id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    year integer NOT NULL UNIQUE CHECK (year BETWEEN 1900 AND 2200)
);

CREATE TABLE budget_items (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    budget_id bigint NOT NULL REFERENCES budgets(id) ON DELETE CASCADE,
    kind      budget_kind NOT NULL,
    name      text NOT NULL CHECK (btrim(name) <> ''),
    amount    numeric(14,2) NOT NULL CHECK (amount >= 0),
    position  integer NOT NULL,
    UNIQUE (id, budget_id)
);
CREATE INDEX idx_budget_items_budget_id ON budget_items(budget_id);

CREATE TABLE budget_item_accounts (
    item_id    bigint NOT NULL,
    budget_id  bigint NOT NULL,
    account_id bigint NOT NULL REFERENCES accounts(id),
    PRIMARY KEY (item_id, account_id),
    -- An account is in one line item per budget.
    UNIQUE (budget_id, account_id),
    FOREIGN KEY (item_id, budget_id) REFERENCES budget_items(id, budget_id) ON DELETE CASCADE
);
CREATE INDEX idx_budget_item_accounts_account_id ON budget_item_accounts(account_id);

-- +goose Down
DROP TABLE budget_item_accounts;
DROP TABLE budget_items;
DROP TABLE budgets;
DROP TYPE budget_kind;

-- +goose Up
-- An account with no type of its own has the type of its parent (and so on up the
-- hierarchy): this says which one, null when nothing up there has one either. The
-- rules that depend on an account's type — an account paid in is an asset, the
-- account of a distribution is income — use this one, not the column.
-- +goose StatementBegin
CREATE FUNCTION account_effective_type(p_id bigint) RETURNS account_type AS $$
    WITH RECURSIVE up AS (
        SELECT id, parent_id, type, 0 AS depth FROM accounts WHERE id = p_id
        UNION ALL
        SELECT a.id, a.parent_id, a.type, up.depth + 1
        FROM accounts a JOIN up ON a.id = up.parent_id
        WHERE up.type IS NULL AND up.depth < 100
    )
    SELECT type FROM up WHERE type IS NOT NULL ORDER BY depth LIMIT 1;
$$ LANGUAGE sql STABLE;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION account_effective_type(bigint);

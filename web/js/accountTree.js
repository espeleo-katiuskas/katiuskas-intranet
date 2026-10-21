// Shared by accounting.js (the chart-of-accounts tree itself) and
// account.js (a single account's own "otra cuenta" picker) — grouping
// GET /accounts's own flat, pre-order list back into parent_id ->
// children preserves each group's own display order exactly, since a
// pre-order walk always visits one parent's whole subtree before its
// next sibling: filtering that flat list down to a single parent_id,
// in the order it already comes in, *is* that parent's sibling order.
// No separate tree-building pass needed.
function childrenByParent(accounts) {
  const map = new Map();
  for (const account of accounts) {
    const key = account.parent_id ?? 'root';
    if (!map.has(key)) map.set(key, []);
    map.get(key).push(account);
  }
  return map;
}

// Flattens the tree into that same depth-first, pre-order order, each
// entry now also carrying its own depth (for indentation) and whether
// it's first/last among its own siblings (for accounting.js's own
// move-up/down buttons).
function flattenAccountTree(accounts) {
  const map = childrenByParent(accounts);
  const result = [];
  function walk(parentKey, depth) {
    const siblings = map.get(parentKey) ?? [];
    siblings.forEach((account, index) => {
      result.push({ account, depth, isFirst: index === 0, isLast: index === siblings.length - 1 });
      walk(account.id, depth + 1);
    });
  }
  walk('root', 0);
  return result;
}

// The accounts to show when some are left out (hidden ones): those for
// which isShown(account) holds, each re-parented onto its nearest shown
// ancestor so a shown account under a hidden one still hangs from
// something in the tree — dropping the hidden row alone would leave its
// children out of the walk altogether, since flattenAccountTree only
// reaches what descends from a root. Order is kept (still pre-order).
function shownAccounts(accounts, isShown) {
  const byId = new Map(accounts.map((account) => [account.id, account]));
  return accounts.filter(isShown).map((account) => {
    let parentId = account.parent_id ?? null;
    while (parentId != null && !isShown(byId.get(parentId)))
      parentId = byId.get(parentId).parent_id ?? null;
    return { ...account, parent_id: parentId ?? undefined };
  });
}

// An account's own name, plus its code in parentheses when it has one
// (see Account's own doc comment — not every account does) — shared
// label text for anywhere an account shows up in a dropdown or a
// heading, so it isn't reformatted slightly differently in each place.
function accountLabel(account) {
  const codeSuffix = account.code ? ` (${account.code})` : '';
  return `${account.name}${codeSuffix}`;
}

export { childrenByParent, flattenAccountTree, shownAccounts, accountLabel };

// A table with class "stack-on-phone" is drawn, on a phone, as a card per row (see .stack-on-phone in
// style.css) instead of being scrolled sideways, which hid its last columns — where the buttons are. A
// card needs to say which column each value is, so every cell gets `data-label` with the text of its
// column's header; the rows are drawn after the page loads, so it is done again whenever they change.
// The header keeps only its sortable columns on a phone (as a row of buttons to sort by).

function headerText(th) {
  // Only the header's own text: not the arrow a sortable one carries in a span.
  return [...th.childNodes]
    .filter((node) => node.nodeType === Node.TEXT_NODE)
    .map((node) => node.textContent)
    .join('')
    .trim();
}

function label(table) {
  const headers = [...table.querySelectorAll('thead th')].map(headerText);
  for (const row of table.querySelectorAll('tbody tr')) {
    [...row.children].forEach((cell, i) => {
      // A cell that spans the row (the "nothing here" message) is not one column's value.
      if (cell.colSpan === 1 && headers[i]) cell.dataset.label = headers[i];
      else delete cell.dataset.label;
    });
  }
}

export function initStackedTables() {
  for (const table of document.querySelectorAll('table.stack-on-phone')) {
    label(table);
    // Only the rows are watched (not the attributes this sets), so it cannot trigger itself.
    new MutationObserver(() => label(table)).observe(table, { childList: true, subtree: true });
  }
}

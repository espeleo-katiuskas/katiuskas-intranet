import { escapeHtml } from './api.js';

// How a piece of material is named, shared by the pages that list it (/material, /material-prestado).

const ropeTypeLabels = { caving: 'Espeleología', canyoning: 'Cañones' };
const bagTypeLabels = {
  caving: 'Espeleología',
  canyoning: 'Cañones',
  quick: 'Rápida',
  special: 'Especial',
};

// How an item reads. A rope/bag is named by brand+model plus its kind-specific attributes, general
// material by the API's display_name (type, brand and model, attribute values, description). Two
// items are never exactly the same, so with the type and the code (when it has one) the name tells
// them apart: there is no "#<id>" any more. includeType is false only in the items table, which has
// its own "Tipo" column, so the type is not repeated there; the code, if any, is part of the label
// everywhere (the table has no column for it).
function itemLabel(item, { includeType = true } = {}) {
  const name = escapeHtml(itemNameText(item, { includeType }));
  return item.code ? `${name} ${escapeHtml(itemRef(item))}` : name;
}

// The same for a picker's <option>: plain text (renderPickerOptions escapes it), the type and the
// code included, as in the items table.
function itemPickerText(item) {
  const name = itemNameText(item, { includeType: true });
  return item.code ? `${name} ${itemRef(item)}` : name;
}

// What tells an item from another that looks the same: its code, "#<code>", or nothing if it has
// none. A code is never only digits.
function itemRef(item) {
  return item.code ? `#${item.code}` : '';
}

function itemNameText(item, { includeType = true } = {}) {
  const brandModel = [item.brand, item.model].filter(Boolean).join(' ');
  if (item.kind === 'rope') {
    const parts = [
      includeType ? item.type_name : null,
      brandModel || null,
      `${item.diameter_mm} mm`,
      ropeTypeLabels[item.rope_type] ?? item.rope_type,
      item.manufacture_year,
      item.length_m ? `${item.length_m}m` : null,
      item.color,
    ];
    return parts.filter(Boolean).join(' · ');
  }
  if (item.kind === 'bag') {
    const parts = [
      includeType ? item.type_name : null,
      brandModel || null,
      `${item.capacity_liters} L`,
      bagTypeLabels[item.bag_type] ?? item.bag_type,
      item.color,
    ];
    return parts.filter(Boolean).join(' · ');
  }
  // General material is named by the API (display_name): its type, make and model, the values of
  // its attributes and its description, joined with " · ".
  const full = item.display_name || item.type_name;
  const prefix = `${item.type_name} · `;
  return !includeType && full.startsWith(prefix) ? full.slice(prefix.length) : full;
}

export { itemLabel, itemPickerText };

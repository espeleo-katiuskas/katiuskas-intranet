import { escapeHtml } from './api.js';

// How a piece of material is named, shared by the pages that list it (/material, /material-prestado).

const ropeTypeLabels = { caving: 'Espeleología', canyoning: 'Cañones' };
const bagTypeLabels = {
  caving: 'Espeleología',
  canyoning: 'Cañones',
  quick: 'Rápida',
  special: 'Especial',
};

// A rope/bag is identified by brand+model (plus its kind-specific
// attributes); a general item may have no known brand/model at all, so its
// type — always set — anchors the label instead. includeType
// is false only in the items table, where the Tipo column already
// shows the item's type and repeating it here would be redundant.
function itemLabel(item, { includeType = true } = {}) {
  return `${itemName(item, { includeType })} ${escapeHtml(itemRef(item))}`;
}

// What tells an item from another that looks the same: "#<code>" or, for an item with no code,
// "#<id>". A code is never only digits, so the two cannot be mistaken.
function itemRef(item) {
  return `#${item.code ?? item.id}`;
}

function itemName(item, { includeType = true } = {}) {
  const brandModel = [item.brand, item.model].filter(Boolean).join(' ');
  if (item.kind === 'rope') {
    const parts = [
      brandModel || null,
      `${item.diameter_mm} mm`,
      ropeTypeLabels[item.rope_type] ?? item.rope_type,
      item.manufacture_year,
      item.length_m ? `${item.length_m} m` : null,
    ];
    return escapeHtml(parts.filter(Boolean).join(' · '));
  }
  if (item.kind === 'bag') {
    const parts = [
      brandModel || null,
      `${item.capacity_liters} L`,
      bagTypeLabels[item.bag_type] ?? item.bag_type,
    ];
    return escapeHtml(parts.filter(Boolean).join(' · '));
  }
  const parts = [includeType ? item.type_name : null, brandModel || null, item.description || null];
  return escapeHtml(parts.filter(Boolean).join(' — '));
}

export { bagTypeLabels, itemLabel, itemRef, ropeTypeLabels };

import { escapeHtml } from './api.js';

// How a piece of material is named, shared by the pages that list it (/material, /material-prestado).

const itemTypeLabels = { rope: 'Cuerda', bag: 'Saca', general: 'Material' };
const ropeTypeLabels = { caving: 'Espeleología', canyoning: 'Cañones' };
const bagTypeLabels = {
  caving: 'Espeleología',
  canyoning: 'Cañones',
  quick: 'Rápida',
  special: 'Especial',
};

// A rope/bag is identified by brand+model (plus its type-specific
// attributes); a general item may have no known brand/model at all, so its
// category — always set — anchors the label instead. includeCategory
// is false only in the items table, where the Tipo column already
// shows a general item's category (see itemTypeColumn) and repeating it
// here would be redundant.
function itemLabel(item, { includeCategory = true } = {}) {
  const brandModel = [item.brand, item.model].filter(Boolean).join(' ');
  if (item.type === 'rope') {
    const parts = [
      brandModel || null,
      `${item.diameter_mm} mm`,
      ropeTypeLabels[item.rope_type] ?? item.rope_type,
      item.manufacture_year,
      item.length_m ? `${item.length_m} m` : null,
    ];
    return escapeHtml(parts.filter(Boolean).join(' · '));
  }
  if (item.type === 'bag') {
    const parts = [
      brandModel || null,
      `${item.capacity_liters} L`,
      bagTypeLabels[item.bag_type] ?? item.bag_type,
    ];
    return escapeHtml(parts.filter(Boolean).join(' · '));
  }
  const parts = [
    includeCategory ? item.category_name : null,
    brandModel || null,
    item.description || null,
  ];
  return escapeHtml(parts.filter(Boolean).join(' — '));
}

export { bagTypeLabels, itemLabel, itemTypeLabels, ropeTypeLabels };

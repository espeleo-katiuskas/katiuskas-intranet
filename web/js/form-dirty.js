// Highlights which fields of a form currently hold a value different from
// what it was last populated with — member.js's and profile.js's own
// "Datos" forms both stay on the page permanently (no dialog to close
// them and lose the edit), so without this there's no visual cue at all
// for which fields an editor actually touched before saving.
//
// A field's own "clean" value lives in its `data-baseline` attribute
// rather than some separate map keyed by field name — reading it straight
// off the element being compared means nothing here needs to know a
// form's own field list, and it survives the field itself being
// disabled/re-enabled (member.js toggles every field's `disabled` for
// board, never removes them from the DOM).
const DIRTY_CLASS = 'field-dirty';

function updateDirty(field) {
  if (field.dataset.baseline === undefined) return;
  field.classList.toggle(DIRTY_CLASS, field.value !== field.dataset.baseline);
}

// Call once per form, right after it's in the DOM — wires a single
// delegated listener rather than one per field, so a field added later
// would still be covered (neither form here does that today, but nothing
// relies on the field list being fixed at call time either).
export function trackDirtyFields(form) {
  form.addEventListener('input', (event) => updateDirty(event.target));
  form.addEventListener('change', (event) => updateDirty(event.target));
}

// Marks every named field's current value as the new "clean" baseline and
// clears any dirty highlight — call this right after (re)populating a
// form: on load, after a successful save, and on "Cancelar".
export function markFieldsClean(form) {
  for (const field of form.elements) {
    if (!field.name) continue;
    field.dataset.baseline = field.value;
    field.classList.remove(DIRTY_CLASS);
  }
}

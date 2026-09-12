// initModal wires the generic, shared parts of turning a <dialog
// class="panel modal"> into a working modal — every page that opens
// one calls this once per dialog, right after grabbing it from the
// DOM. It never opens the dialog itself (that's page-specific: a
// static "+ Nueva X" button calling showModal() directly, or, for a
// per-row edit dialog, some async fetch filling in that row's own
// data first — see account.js/accounting.js/member.js) — only what
// every dialog needs regardless of how it opens:
//
// - every element inside marked data-modal-close (the "×" in the
//   corner, and a form's own "Cancelar" button alike) closes it;
// - clicking the backdrop itself does too — a click that lands on the
//   dialog element's own box rather than bubbling from something
//   inside it is exactly a backdrop click, the standard way to detect
//   one for a native <dialog>;
// - Escape already closes a native <dialog> on its own, firing
//   "cancel" then "close" — nothing to wire for that.
//
// form.reset() runs once, on the dialog's own "close" event rather
// than in every single button's own click handler — "close" fires
// regardless of *how* the dialog closed (×, Cancelar, a backdrop
// click, Escape, or a successful save that calls dialog.close() itself
// once it's done), so this is the one place that needs to happen, and
// it also means a caller's own submit handler no longer has to reset
// the form itself after a successful save.
function initModal(dialog, form) {
  if (form) {
    dialog.addEventListener('close', () => form.reset());
  }
  dialog.querySelectorAll('[data-modal-close]').forEach((button) => {
    button.addEventListener('click', () => dialog.close());
  });
  dialog.addEventListener('click', (event) => {
    if (event.target === dialog) dialog.close();
  });
}

export { initModal };

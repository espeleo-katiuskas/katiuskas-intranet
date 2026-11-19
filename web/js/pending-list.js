import { apiFetch, apiFetchForm, escapeHtml, isOffline, showError, showFeedback } from './api.js';
import * as outbox from './outbox.js';

// The entries of the outbox that belong to a page, drawn in `container` as "Pendiente de enviar",
// each with what it is (`describe(entry)`, html-safe text), why the server refused it if it did, and
// "Reintentar" and "Descartar". `matches(entry)` says which ones are of this page; `reload()` draws
// the page's own data again when something was sent. Returns the function that draws the list.
export function bindPendingList({ container, login, matches, describe, reload }) {
  if (!container) return async () => {};

  async function render() {
    let pending = [];
    try {
      pending = await outbox.entries((e) => e.login === login && matches(e));
    } catch {
      // No storage (a private window): nothing is waiting.
    }
    container.innerHTML = pending
      .map(
        (e) => `
    <div class="outbox-row">
      <div><span class="badge">Pendiente de enviar</span> <strong>${escapeHtml(describe(e))}</strong></div>
      ${e.error ? `<p class="form-hint">No se pudo enviar: ${escapeHtml(e.error)}</p>` : ''}
      <div class="outbox-actions">
        <button type="button" class="btn btn-secondary btn-small" data-outbox-retry="${e.id}"${
          isOffline() ? ' hidden' : ''
        }>Reintentar</button>
        <button type="button" class="btn btn-secondary btn-small" data-outbox-discard="${e.id}">Descartar</button>
      </div>
    </div>`,
      )
      .join('');
  }

  container.addEventListener('click', async (event) => {
    const retry = event.target.closest('[data-outbox-retry]');
    const discard = event.target.closest('[data-outbox-discard]');
    if (discard) {
      if (!confirm('¿Descartar esto sin enviarlo?')) return;
      await outbox.discard(Number(discard.dataset.outboxDiscard));
    } else if (retry) {
      await outbox.update(Number(retry.dataset.outboxRetry), { error: null });
      try {
        const sent = await outbox.flush(login, { apiFetch, apiFetchForm });
        if (sent.length) showFeedback('Enviado.');
      } catch (err) {
        showError(err.message);
      }
      await reload?.();
    } else {
      return;
    }
    await render();
  });

  // layout.js sends the outbox when the page opens; when something went, show it.
  window.addEventListener('katiuskas:outbox-sent', () => {
    render();
    reload?.();
  });

  return render;
}

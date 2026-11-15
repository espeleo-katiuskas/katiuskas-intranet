import {
  API_BASE,
  apiFetch,
  apiFetchForm,
  escapeHtml,
  formatDateTime,
  showError,
  showFeedback,
} from './api.js';
import { formatSize } from './file-browser.js';

// The photos and documents of a place and of a system are the same thing with another address: `base`
// is "/places/3" or "/systems/4", and under it are the files (GET and POST /files, the bytes at
// /files/{id}/file, DELETE /files/{id}). This draws them and makes the form to add one and the buttons to
// remove one work, for both pages and for the system on the page of each of its cavities.

function fileUrl(base, file, download = false) {
  return `${API_BASE}${base}/files/${file.id}/file${download ? '?download=1' : ''}`;
}

// The files as rows with a preview of an image: each with its title (or its name) as a link, its name,
// size, who attached it and when, "Descargar" and, when `canRemove(file)` says so, "Borrar".
function fileRowsHtml(files, { base, canRemove = () => false, personLink }) {
  if (!files.length) return '<p>Todavía no hay fotos ni documentos.</p>';
  return files
    .map((f) => {
      const label = f.title || f.file_name;
      const preview = f.content_type.startsWith('image/')
        ? `<a href="${fileUrl(base, f)}" target="_blank" rel="noopener"><img src="${fileUrl(base, f)}" alt="${escapeHtml(label)}" class="resource-image" loading="lazy"></a>`
        : '';
      return `
        <div class="comment">
          <div class="comment-head">
            <span><a href="${fileUrl(base, f)}" target="_blank" rel="noopener"><strong>${escapeHtml(label)}</strong></a>
              <span class="subtitle">${escapeHtml(f.file_name)} · ${formatSize(f.file_size)} · ${personLink(f.uploaded_by)}, ${formatDateTime(f.created_at)}</span></span>
            <span class="nowrap">
              <a class="btn btn-secondary btn-small" href="${fileUrl(base, f, true)}">Descargar</a>
              ${canRemove(f) ? `<button type="button" class="btn btn-secondary btn-small" data-delete-file="${f.id}">Borrar</button>` : ''}
            </span>
          </div>
          ${preview}
        </div>`;
    })
    .join('');
}

// Makes the form to add a file and the buttons "Borrar" inside `list` work, then calls `reload`.
function bindFiles({ list, form, base, reload }) {
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const submit = form.querySelector('button[type="submit"]');
    submit.disabled = true;
    try {
      await apiFetchForm(`${base}/files`, new FormData(form));
      form.reset();
      showFeedback('Fichero añadido.');
      await reload();
    } catch (err) {
      showError(err.message);
    } finally {
      submit.disabled = false;
    }
  });
  list.addEventListener('click', async (event) => {
    const button = event.target.closest('button[data-delete-file]');
    if (!button || !confirm('¿Quitar este fichero?')) return;
    try {
      await apiFetch(`${base}/files/${button.dataset.deleteFile}`, { method: 'DELETE' });
      showFeedback('Fichero quitado.');
      await reload();
    } catch (err) {
      showError(err.message);
    }
  });
}

export { bindFiles, fileRowsHtml, fileUrl };

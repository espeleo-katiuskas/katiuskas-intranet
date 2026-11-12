import { apiFetchForm, escapeHtml, formatDateTime } from './api.js';
import { formatSize } from './file-browser.js';

// What the pages share about the documents (tickets, invoices) attached to a transaction.

// The address of a file. The API accepts the session cookie there, so a plain link opens it in a
// tab (a PDF or an image is shown, anything else is downloaded).
function transactionFileUrl(transactionId, fileId, { download = false } = {}) {
  return `/api/v1/transactions/${transactionId}/files/${fileId}/file${download ? '?download=1' : ''}`;
}

function transactionFileLink(file) {
  const url = transactionFileUrl(file.transaction_id, file.id);
  return `<a href="${url}" target="_blank" rel="noopener">${escapeHtml(file.file_name)}</a> <span class="hint">(${formatSize(file.file_size)})</span>`;
}

// "📎 2" next to a transaction that has files, nothing if it has none.
function fileCountBadge(count) {
  if (!count) return '';
  const label = count === 1 ? '1 fichero adjunto' : `${count} ficheros adjuntos`;
  return ` <span class="file-badge" title="${label}" aria-label="${label}">📎${count > 1 ? ` ${count}` : ''}</span>`;
}

// The files of a transaction as list items; `removable` adds a "Quitar" button to each.
function transactionFileItems(files, { removable = false } = {}) {
  return files
    .map(
      (file) => `<li data-file-id="${file.id}">${transactionFileLink(file)}
        <span class="hint">· ${escapeHtml(formatDateTime(file.created_at))}${file.uploaded_by ? ` · ${escapeHtml(file.uploaded_by)}` : ''}</span>
        ${removable ? `<button type="button" class="btn btn-secondary btn-small" data-remove-file="${file.id}">Quitar</button>` : ''}</li>`,
    )
    .join('');
}

// Uploads files one by one to a transaction; answers the messages of the ones that failed.
async function uploadTransactionFiles(transactionId, files) {
  const failures = [];
  for (const file of files) {
    const form = new FormData();
    form.append('file', file);
    try {
      await apiFetchForm(`/transactions/${transactionId}/files`, form);
    } catch (err) {
      failures.push(`${file.name}: ${err.message}`);
    }
  }
  return failures;
}

export { fileCountBadge, transactionFileItems, transactionFileLink, uploadTransactionFiles };

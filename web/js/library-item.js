import {
  API_BASE,
  apiFetch,
  apiFetchForm,
  escapeHtml,
  formatDateTime,
  hasAnyRole,
  isNotFound,
  queryId,
  resolvePickerId,
  showError,
  showFeedback,
  showNotFound,
  stopAsNotFound,
} from './api.js';
import {
  coverImage,
  dateOnly,
  loadPeople,
  END_REASONS,
  loadTypes,
  personLink,
  statusBadge,
  today,
  typeOptions,
} from './library-common.js';
import { renderRichText } from './rich-text.js';
import { formatSize } from './file-browser.js';
import { initPage } from './layout.js';
import { initModal, openModal } from './modal.js';

const user = await initPage();
const canManage = hasAnyRole(user, ['admin', 'board', 'biblioteca']);
const canLoan = hasAnyRole(user, ['admin', 'board', 'biblioteca', 'material']);
const isAdmin = hasAnyRole(user, ['admin']);

const itemId = queryId();
if (itemId === null) await stopAsNotFound();

let item = null;
let comments = [];
let resources = [];
let loans = [];
let people = { namesByLogin: {}, memberEntries: [], memberOptions: '' };

function nameOf(login) {
  return people.namesByLogin[login] ?? login;
}

// ----- the item -----

function renderItem() {
  document.title = `${item.title} — Club de Espeleología Katiuskas`;
  document.getElementById('library-title').textContent = item.title;
  document.getElementById('library-meta').textContent = [
    item.type,
    item.author,
    item.publisher,
    item.year,
  ]
    .filter(Boolean)
    .join(' · ');
  document.getElementById('library-status').innerHTML = statusBadge(item);
  document.getElementById('library-cover').innerHTML = item.has_cover
    ? `<a href="${API_BASE}/library/items/${item.id}/cover" target="_blank" rel="noopener">${coverImage(item, 'library-cover-image')}</a>`
    : '';
  const rows = [
    ['Adquirido el', dateOnly(item.acquired_on)],
    [
      'Baja',
      item.ended_on ? `${END_REASONS[item.end_reason]} el ${dateOnly(item.ended_on)}` : null,
    ],
    ['ISBN', item.isbn],
    ['Dónde está', item.location],
    ['Descripción', item.description],
  ].filter(([, value]) => value);
  document.getElementById('library-details').innerHTML = rows
    .map(
      ([label, value]) =>
        `<div class="detail-row"><span class="subtitle">${label}</span>${
          label === 'Descripción'
            ? `<div class="rich-text">${renderRichText(value)}</div>`
            : `<span>${escapeHtml(value)}</span>`
        }</div>`,
    )
    .join('');
  // The buttons only exist for those who manage the library (data-roles removes them for the rest).
  if (canManage) {
    document.getElementById('remove-cover').hidden = !item.has_cover;
    document.getElementById('change-cover').textContent = item.has_cover
      ? 'Cambiar portada'
      : 'Añadir portada';
  }
}

// ----- comments -----

function renderComments() {
  const box = document.getElementById('comments');
  box.innerHTML = comments.length
    ? comments
        .map((c) => {
          const mine = c.user_login === user.login;
          return `
        <div class="comment">
          <div class="comment-head">
            <span>${personLink(people.namesByLogin, c.user_login)} <span class="subtitle">${formatDateTime(c.created_at)}</span></span>
            ${mine || canManage ? `<button type="button" class="btn btn-secondary btn-small" data-delete-comment="${c.id}">Borrar</button>` : ''}
          </div>
          <div class="comment-body rich-text">${renderRichText(c.body)}</div>
        </div>`;
        })
        .join('')
    : '<p>Todavía no hay comentarios.</p>';
}

// ----- files -----

function fileUrl(resource, download = false) {
  return `${API_BASE}/library/items/${itemId}/resources/${resource.id}/file${download ? '?download=1' : ''}`;
}

function renderResources() {
  const box = document.getElementById('resources');
  box.innerHTML = resources.length
    ? resources
        .map((r) => {
          const mine = r.uploaded_by === user.login;
          const label = r.title || r.file_name;
          const preview = r.content_type.startsWith('image/')
            ? `<a href="${fileUrl(r)}" target="_blank" rel="noopener"><img src="${fileUrl(r)}" alt="${escapeHtml(label)}" class="resource-image" loading="lazy"></a>`
            : '';
          return `
        <div class="comment">
          <div class="comment-head">
            <span><a href="${fileUrl(r)}" target="_blank" rel="noopener"><strong>${escapeHtml(label)}</strong></a>
              <span class="subtitle">${escapeHtml(r.file_name)} · ${formatSize(r.file_size)} · ${personLink(people.namesByLogin, r.uploaded_by)}, ${formatDateTime(r.created_at)}</span></span>
            <span class="nowrap">
              <a class="btn btn-secondary btn-small" href="${fileUrl(r, true)}">Descargar</a>
              ${mine || canManage ? `<button type="button" class="btn btn-secondary btn-small" data-delete-resource="${r.id}">Borrar</button>` : ''}
            </span>
          </div>
          ${preview}
        </div>`;
        })
        .join('')
    : '<p>Todavía no hay fotos ni documentos.</p>';
}

// ----- loans -----

function renderLoans() {
  if (!canLoan) return;
  const open = loans.find((l) => !l.returned_at);
  let state = 'El ejemplar está en el local.';
  if (open) {
    state = `Prestado a ${personLink(people.namesByLogin, open.user_login)} desde el ${dateOnly(open.lent_at)}.`;
  } else if (item.ended_on) {
    state = `El ejemplar ya no está en la biblioteca (${END_REASONS[item.end_reason].toLowerCase()} el ${dateOnly(item.ended_on)}): no se puede prestar.`;
  }
  document.getElementById('loan-status').innerHTML = state;
  document.getElementById('lend-form').hidden = Boolean(open) || Boolean(item.ended_on);
  document.getElementById('loans-table-body').innerHTML = loans.length
    ? loans
        .map(
          (l) => `
      <tr>
        <td>${personLink(people.namesByLogin, l.user_login)}</td>
        <td class="nowrap">${dateOnly(l.lent_at)}</td>
        <td class="nowrap">${l.returned_at ? dateOnly(l.returned_at) : '—'}</td>
        <td>${escapeHtml(nameOf(l.recorded_by_login))}</td>
        <td>${l.notes ? escapeHtml(l.notes) : ''}</td>
        <td class="nowrap">
          ${l.returned_at ? '' : `<button type="button" class="btn btn-secondary btn-small" data-return-loan="${l.id}">Devolver</button>`}
          ${isAdmin ? `<button type="button" class="btn btn-danger btn-small" data-delete-loan="${l.id}">Borrar</button>` : ''}
        </td>
      </tr>`,
        )
        .join('')
    : '<tr><td colspan="6">Este ejemplar no se ha prestado nunca.</td></tr>';
}

function renderAll() {
  renderItem();
  renderComments();
  renderResources();
  renderLoans();
}

async function load() {
  try {
    const requests = [
      apiFetch(`/library/items/${itemId}`),
      apiFetch(`/library/items/${itemId}/comments`),
      apiFetch(`/library/items/${itemId}/resources`),
      loadPeople(),
    ];
    if (canLoan) requests.push(apiFetch(`/library/items/${itemId}/loans`));
    const [loadedItem, loadedComments, loadedResources, loadedPeople, loadedLoans] =
      await Promise.all(requests);
    item = loadedItem;
    comments = loadedComments;
    resources = loadedResources;
    people = loadedPeople;
    loans = loadedLoans ?? [];
    if (canLoan) document.getElementById('loan-people-options').innerHTML = people.memberOptions;
    renderAll();
  } catch (err) {
    if (isNotFound(err)) {
      showNotFound();
      return;
    }
    showError(err.message);
  }
}

// Runs a change, then loads everything again; the error, if any, is shown.
async function change(action, feedback) {
  try {
    await action();
    if (feedback) showFeedback(feedback);
    await load();
    return true;
  } catch (err) {
    showError(err.message);
    return false;
  }
}

// ----- comments and files: anybody -----

document.getElementById('comment-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  const ok = await change(
    () =>
      apiFetch(`/library/items/${itemId}/comments`, {
        method: 'POST',
        body: JSON.stringify({ body: form.elements.body.value }),
      }),
    'Comentario añadido.',
  );
  if (ok) form.reset();
});

document.getElementById('comments').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-delete-comment]');
  if (!button || !confirm('¿Borrar este comentario?')) return;
  await change(
    () =>
      apiFetch(`/library/items/${itemId}/comments/${button.dataset.deleteComment}`, {
        method: 'DELETE',
      }),
    'Comentario borrado.',
  );
});

document.getElementById('resource-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  const submit = form.querySelector('button[type="submit"]');
  submit.disabled = true;
  const ok = await change(
    () => apiFetchForm(`/library/items/${itemId}/resources`, new FormData(form)),
    'Fichero añadido.',
  );
  submit.disabled = false;
  if (ok) form.reset();
});

document.getElementById('resources').addEventListener('click', async (event) => {
  const button = event.target.closest('button[data-delete-resource]');
  if (!button || !confirm('¿Quitar este fichero?')) return;
  await change(
    () =>
      apiFetch(`/library/items/${itemId}/resources/${button.dataset.deleteResource}`, {
        method: 'DELETE',
      }),
    'Fichero quitado.',
  );
});

// ----- the Junta and the library: the item and its cover -----

if (canManage) {
  const dialog = document.getElementById('edit-item-dialog');
  const form = document.getElementById('edit-item-form');
  initModal(dialog, form);
  document.getElementById('edit-item').addEventListener('click', async () => {
    try {
      form.elements.type_id.innerHTML = typeOptions(await loadTypes(), item.type_id);
    } catch (err) {
      showError(err.message);
      return;
    }
    form.elements.acquired_on.max = today();
    form.elements.ended_on.max = today();
    form.elements.acquired_on.value = item.acquired_on;
    form.elements.ended_on.value = item.ended_on ?? '';
    form.elements.end_reason.value = item.end_reason ?? '';
    form.elements.title.value = item.title;
    form.elements.author.value = item.author ?? '';
    form.elements.publisher.value = item.publisher ?? '';
    form.elements.year.value = item.year ?? '';
    form.elements.isbn.value = item.isbn ?? '';
    form.elements.location.value = item.location ?? '';
    form.elements.description.value = item.description ?? '';
    openModal(dialog);
  });
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const text = (name) => form.elements[name].value.trim() || null;
    const ok = await change(
      () =>
        apiFetch(`/library/items/${itemId}`, {
          method: 'PUT',
          body: JSON.stringify({
            type_id: Number(form.elements.type_id.value),
            acquired_on: form.elements.acquired_on.value,
            ended_on: form.elements.ended_on.value || null,
            end_reason: form.elements.end_reason.value || null,
            title: form.elements.title.value.trim(),
            author: text('author'),
            publisher: text('publisher'),
            year: form.elements.year.value ? Number(form.elements.year.value) : null,
            isbn: text('isbn'),
            location: text('location'),
            description: text('description'),
          }),
        }),
      'Ejemplar guardado.',
    );
    if (ok) dialog.close();
  });

  document.getElementById('delete-item').addEventListener('click', async () => {
    if (!confirm(`¿Borrar «${item.title}», con sus comentarios y ficheros?`)) return;
    try {
      await apiFetch(`/library/items/${itemId}`, { method: 'DELETE' });
      window.location.href = '/biblioteca';
    } catch (err) {
      showError(err.message);
    }
  });

  const coverInput = document.getElementById('cover-input');
  document.getElementById('change-cover').addEventListener('click', () => coverInput.click());
  coverInput.addEventListener('change', async () => {
    if (!coverInput.files.length) return;
    const data = new FormData();
    data.append('file', coverInput.files[0]);
    await change(
      () => apiFetchForm(`/library/items/${itemId}/cover`, data, { method: 'PUT' }),
      'Portada guardada.',
    );
    coverInput.value = '';
  });
  document.getElementById('remove-cover').addEventListener('click', async () => {
    if (!confirm('¿Quitar la portada?')) return;
    await change(
      () => apiFetch(`/library/items/${itemId}/cover`, { method: 'DELETE' }),
      'Portada quitada.',
    );
  });
}

// ----- the loans -----

if (canLoan) {
  const lendForm = document.getElementById('lend-form');
  const lendDate = document.getElementById('lend-date');
  const returnDialog = document.getElementById('return-dialog');
  const returnForm = document.getElementById('return-form');
  const returnDate = document.getElementById('return-date');
  initModal(returnDialog, returnForm);
  let returningLoanId = null;

  lendDate.max = today();
  lendDate.value = today();
  lendForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    const login = resolvePickerId(
      people.memberEntries,
      document.getElementById('lend-person').value,
    );
    if (!login) {
      showError('Elige un socio de la lista.');
      return;
    }
    const ok = await change(
      () =>
        apiFetch(`/library/items/${itemId}/loans`, {
          method: 'POST',
          body: JSON.stringify({
            login,
            lent_at: lendDate.value,
            notes: document.getElementById('lend-notes').value.trim() || null,
          }),
        }),
      'Préstamo apuntado.',
    );
    if (ok) {
      lendForm.reset();
      lendDate.value = today();
    }
  });

  document.getElementById('loans-table-body').addEventListener('click', async (event) => {
    const returnButton = event.target.closest('button[data-return-loan]');
    if (returnButton) {
      returningLoanId = returnButton.dataset.returnLoan;
      const lent = loans.find((l) => String(l.id) === returningLoanId)?.lent_at;
      returnDate.min = lent ?? '';
      returnDate.max = today();
      returnDate.value = today();
      openModal(returnDialog);
      return;
    }
    const deleteButton = event.target.closest('button[data-delete-loan]');
    if (
      deleteButton &&
      confirm('¿Borrar este préstamo del historial? Es para uno apuntado por error.')
    ) {
      await change(
        () =>
          apiFetch(`/library/items/${itemId}/loans/${deleteButton.dataset.deleteLoan}`, {
            method: 'DELETE',
          }),
        'Préstamo borrado.',
      );
    }
  });

  returnForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    const ok = await change(
      () =>
        apiFetch(`/library/items/${itemId}/loans/${returningLoanId}/return`, {
          method: 'POST',
          body: JSON.stringify({ returned_at: returnDate.value }),
        }),
      'Devolución apuntada.',
    );
    if (ok) returnDialog.close();
  });
}

await load();

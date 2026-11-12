import {
  apiFetch,
  apiFetchBlob,
  escapeHtml,
  encodeFilePath,
  formatDate,
  showError,
} from './api.js';
import { renderMarkdown } from './markdown.js';
import {
  joinPath,
  dirOf,
  nameOf,
  formatSize,
  fileKind,
  resolveRelativePath,
  downloadBlob,
} from './file-browser.js';

// A per-person document browser, embedded (not a page of its own) in
// both perfil.html (a socio's own files) and socio.html (admin/board
// viewing someone else's) — see GET /users/{login}/files[/file]. Mostly
// mirrors js/archive.js's own browsing/preview logic (see
// js/file-browser.js for the pieces shared with it), but navigates by
// updating this same page's DOM in place instead of real page loads:
// there's no dedicated URL for "this folder" or "this file" the way
// /archivo?path=/?view= has, since this widget only ever lives inside a
// bigger page that already owns the URL (login query string, or the
// signed-in viewer). Internal links use a synthetic #uf:dir:.../
// #uf:file:... href instead, intercepted the same way breadcrumbs and
// entries are.

// A synthetic, non-navigating href this widget's own click handlers
// intercept — see the module doc comment above for why this can't just
// be a real URL the way archive.js's linkHrefFor is.
function ufHref(path, isDir) {
  return `#uf:${isDir ? 'dir' : 'file'}:${encodeURIComponent(path)}`;
}

function parseUfHref(href) {
  const match = href.match(/^#uf:(dir|file):(.*)$/);
  return match ? { isDir: match[1] === 'dir', path: decodeURIComponent(match[2]) } : null;
}

// initUserFilesBrowser wires up a fixed set of element ids (uf-*, see
// perfil.html/socio.html) to browse and preview the given login's
// files. Safe to call on a page that doesn't have that markup — it
// simply does nothing. hideWhenEmpty hides the whole #uf-section
// wrapper (if present) when this person's own root has no files at
// all yet — perfil.html uses this so "Mis ficheros" doesn't show up
// as an empty section for the common case of nobody having dropped
// anything in; socio.html leaves it off, since admin/board still want
// to see the (empty) section to know there's nothing there.
function initUserFilesBrowser(login, { hideWhenEmpty = false } = {}) {
  const breadcrumbsEl = document.getElementById('uf-breadcrumbs');
  const directoryView = document.getElementById('uf-directory-view');
  const fileView = document.getElementById('uf-file-view');
  const entriesList = document.getElementById('uf-entries-list');
  const section = document.getElementById('uf-section');
  if (!breadcrumbsEl || !directoryView || !fileView || !entriesList) return;

  function renderBreadcrumbs(dirPath) {
    const segments = dirPath ? dirPath.split('/') : [];
    const links = [`<a href="${ufHref('', true)}">Ficheros</a>`];
    let acc = '';
    for (const segment of segments) {
      acc = joinPath(acc, segment);
      links.push(`<a href="${ufHref(acc, true)}">${escapeHtml(segment)}</a>`);
    }
    breadcrumbsEl.innerHTML = links.join(' / ');
  }

  function renderEntryRow(dirPath, entry) {
    const full = joinPath(dirPath, entry.name);
    const isDir = entry.type === 'dir';
    const label = isDir ? `${entry.name}/` : entry.name;
    return `
      <tr>
        <td><a href="${ufHref(full, isDir)}">${escapeHtml(label)}</a></td>
        <td>${isDir ? '' : formatSize(entry.size)}</td>
        <td>${formatDate(entry.modified_at)}</td>
      </tr>
    `;
  }

  async function loadDirectory(dirPath) {
    directoryView.hidden = false;
    fileView.hidden = true;
    renderBreadcrumbs(dirPath);
    entriesList.innerHTML = '<tr><td colspan="3">Cargando…</td></tr>';
    try {
      const listing = await apiFetch(`/users/${login}/files?path=${encodeURIComponent(dirPath)}`);
      if (hideWhenEmpty && section && dirPath === '') {
        section.hidden = listing.entries.length === 0;
      }
      entriesList.innerHTML = listing.entries.length
        ? listing.entries.map((entry) => renderEntryRow(listing.path, entry)).join('')
        : '<tr><td colspan="3">Todavía no hay ficheros aquí.</td></tr>';
    } catch (err) {
      entriesList.innerHTML = `<tr><td colspan="3">${escapeHtml(err.message)}</td></tr>`;
    }
  }

  async function loadFile(path) {
    directoryView.hidden = true;
    fileView.hidden = false;
    const dir = dirOf(path);
    const name = nameOf(path);
    renderBreadcrumbs(dir);

    document.getElementById('uf-file-name').textContent = name;
    document.getElementById('uf-file-meta').textContent = 'Cargando…';
    const preview = document.getElementById('uf-file-preview');
    preview.innerHTML = '';

    document.getElementById('uf-file-back').onclick = () => loadDirectory(dir);
    document.getElementById('uf-file-download').onclick = async () => {
      try {
        const blob = await apiFetchBlob(
          `/users/${login}/files/file/${encodeFilePath(path)}?download=1`,
        );
        downloadBlob(blob, name);
      } catch (err) {
        showError(err.message);
      }
    };

    const kind = fileKind(name);
    try {
      const blob = await apiFetchBlob(`/users/${login}/files/file/${encodeFilePath(path)}`);
      document.getElementById('uf-file-meta').textContent = formatSize(blob.size);

      if (kind === 'markdown' || kind === 'text') {
        const text = await blob.text();
        if (kind === 'markdown') {
          preview.innerHTML = renderMarkdown(text, (href) => {
            const resolved = resolveRelativePath(dir, href);
            return resolved ? ufHref(resolved.path, resolved.isDir) : href;
          });
        } else {
          const pre = document.createElement('pre');
          pre.className = 'document-text';
          pre.textContent = text;
          preview.appendChild(pre);
        }
      } else if (kind === 'pdf') {
        const url = URL.createObjectURL(blob);
        preview.innerHTML = `<embed src="${url}" type="application/pdf" class="document-embed">`;
      } else if (kind === 'image') {
        const url = URL.createObjectURL(blob);
        preview.innerHTML = `<img src="${url}" alt="${escapeHtml(name)}" class="document-image">`;
      } else if (kind === 'html') {
        // Fully sandboxed, same reasoning as archive.js: no safe way to
        // intercept navigation inside a sandboxed frame, so relative
        // links inside it aren't rewritten into in-widget navigation.
        const url = URL.createObjectURL(blob);
        preview.innerHTML = `<iframe src="${url}" class="document-frame" sandbox=""></iframe>`;
      } else {
        preview.innerHTML =
          '<p class="hint">No hay vista previa disponible para este tipo de fichero. Descárgalo para verlo.</p>';
      }
    } catch (err) {
      document.getElementById('uf-file-meta').textContent = '';
      showError(err.message);
    }
  }

  function handleNavClick(event) {
    const link = event.target.closest('a[href^="#uf:"]');
    if (!link) return;
    event.preventDefault();
    const parsed = parseUfHref(link.getAttribute('href'));
    if (!parsed) return;
    if (parsed.isDir) loadDirectory(parsed.path);
    else loadFile(parsed.path);
  }
  breadcrumbsEl.addEventListener('click', handleNavClick);
  entriesList.addEventListener('click', handleNavClick);
  fileView.addEventListener('click', handleNavClick);

  loadDirectory('');
}

export { initUserFilesBrowser };

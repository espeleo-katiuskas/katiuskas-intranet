import { apiFetch, apiFetchBlob, escapeHtml, encodeFilePath, formatDate } from './api.js';
import { renderMarkdown } from './markdown.js';

// A per-person document browser, embedded (not a page of its own) in
// both profile.html (a socio's own files) and member.html (admin/board
// viewing someone else's) — see GET /users/{login}/files[/file]. Mostly
// mirrors js/library.js's own browsing/preview logic, but navigates by
// updating this same page's DOM in place instead of real page loads:
// there's no dedicated URL for "this folder" or "this file" the way
// /library?path=/?view= has, since this widget only ever lives inside a
// bigger page that already owns the URL (login query string, or the
// signed-in viewer). Internal links use a synthetic #uf:dir:.../
// #uf:file:... href instead, intercepted the same way breadcrumbs and
// entries are.

function joinPath(dir, name) {
  return dir ? `${dir}/${name}` : name;
}

function dirOf(path) {
  const i = path.lastIndexOf('/');
  return i === -1 ? '' : path.slice(0, i);
}

function nameOf(path) {
  const i = path.lastIndexOf('/');
  return i === -1 ? path : path.slice(i + 1);
}

function formatSize(bytes) {
  if (bytes == null) return '';
  const units = ['B', 'KB', 'MB', 'GB'];
  let value = bytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex++;
  }
  return `${value.toFixed(unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

function fileKind(name) {
  const ext = name.toLowerCase().split('.').pop();
  if (['md', 'markdown'].includes(ext)) return 'markdown';
  if (['html', 'htm'].includes(ext)) return 'html';
  if (ext === 'txt') return 'text';
  if (ext === 'pdf') return 'pdf';
  if (['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp'].includes(ext)) return 'image';
  return 'other';
}

// Same document-relative link resolution as js/library.js — "./Actas/",
// "../foo.pdf" and the like, resolved against the directory the
// document viewing it lives in. Anything that isn't a document-relative
// link (an absolute URL, mailto:, a same-page # anchor) is left
// untouched.
function resolveRelativePath(baseDir, href) {
  if (/^([a-z][a-z0-9+.-]*:)?\/\//i.test(href) || href.startsWith('mailto:') || href.startsWith('#')) {
    return null;
  }
  const isDir = href.endsWith('/');
  const parts = [];
  for (const seg of (baseDir ? baseDir.split('/') : []).concat(href.split('/'))) {
    if (seg === '' || seg === '.') continue;
    if (seg === '..') {
      parts.pop();
      continue;
    }
    parts.push(seg);
  }
  return { path: parts.join('/'), isDir };
}

// A synthetic, non-navigating href this widget's own click handlers
// intercept — see the module doc comment above for why this can't just
// be a real URL the way library.js's linkHrefFor is.
function ufHref(path, isDir) {
  return `#uf:${isDir ? 'dir' : 'file'}:${encodeURIComponent(path)}`;
}

function parseUfHref(href) {
  const match = href.match(/^#uf:(dir|file):(.*)$/);
  return match ? { isDir: match[1] === 'dir', path: decodeURIComponent(match[2]) } : null;
}

function downloadBlob(blob, filename) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// initUserFilesBrowser wires up a fixed set of element ids (uf-*, see
// profile.html/member.html) to browse and preview the given login's
// files. Safe to call on a page that doesn't have that markup — it
// simply does nothing. hideWhenEmpty hides the whole #uf-section
// wrapper (if present) when this person's own root has no files at
// all yet — profile.html uses this so "Mis ficheros" doesn't show up
// as an empty section for the common case of nobody having dropped
// anything in; member.html leaves it off, since admin/board still want
// to see the (empty) section to know there's nothing there.
function initUserFilesBrowser(login, { hideWhenEmpty = false } = {}) {
  const breadcrumbsEl = document.getElementById('uf-breadcrumbs');
  const directoryView = document.getElementById('uf-directory-view');
  const fileView = document.getElementById('uf-file-view');
  const entriesList = document.getElementById('uf-entries-list');
  const section = document.getElementById('uf-section');
  if (!breadcrumbsEl || !directoryView || !fileView || !entriesList) return;

  function showError(message) {
    const feedbackBox = document.getElementById('feedback');
    const errorBox = document.getElementById('error');
    if (feedbackBox) feedbackBox.hidden = true;
    if (errorBox) {
      errorBox.textContent = message;
      errorBox.hidden = false;
    }
  }

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
        const blob = await apiFetchBlob(`/users/${login}/files/file/${encodeFilePath(path)}?download=1`);
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
        // Fully sandboxed, same reasoning as library.js: no safe way to
        // intercept navigation inside a sandboxed frame, so relative
        // links inside it aren't rewritten into in-widget navigation.
        const url = URL.createObjectURL(blob);
        preview.innerHTML = `<iframe src="${url}" class="document-frame" sandbox=""></iframe>`;
      } else {
        preview.innerHTML = '<p class="hint">No hay vista previa disponible para este tipo de fichero. Descárgalo para verlo.</p>';
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

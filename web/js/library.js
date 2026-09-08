import {
  initSession,
  logout,
  applyRoleVisibility,
  initSidebarToggle,
  loadTopbarGreeting,
  apiFetch,
  apiFetchBlob,
  escapeHtml,
  encodeFilePath,
  formatDate,
} from './api.js';
import { renderMarkdown } from './markdown.js';

await initSession();
applyRoleVisibility();
document.getElementById('logout-button').addEventListener('click', logout);
initSidebarToggle();
loadTopbarGreeting();

const feedbackBox = document.getElementById('feedback');
const errorBox = document.getElementById('error');

function showError(message) {
  feedbackBox.hidden = true;
  errorBox.textContent = message;
  errorBox.hidden = false;
}

const directoryView = document.getElementById('directory-view');
const fileView = document.getElementById('file-view');
const entriesList = document.getElementById('entries-list');
const breadcrumbsEl = document.getElementById('breadcrumbs');

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

// Resolves a link found inside a rendered document against the
// directory it lives in — "./Actas/", "../Estatutos.pdf" and the like —
// into a { path, isDir } pair, or null for anything that isn't a
// document-relative link (an absolute URL, a mailto:, a same-page #
// anchor), which is left untouched. A trailing "/" is how a document's
// author marks a link as pointing to a folder rather than a file.
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

function linkHrefFor(path, isDir) {
  return isDir ? `/library?path=${encodeURIComponent(path)}` : `/library?view=${encodeURIComponent(path)}`;
}

function renderBreadcrumbs(dirPath) {
  const segments = dirPath ? dirPath.split('/') : [];
  const links = ['<a href="/library">Documentos</a>'];
  let acc = '';
  for (const segment of segments) {
    acc = joinPath(acc, segment);
    links.push(`<a href="/library?path=${encodeURIComponent(acc)}">${escapeHtml(segment)}</a>`);
  }
  breadcrumbsEl.innerHTML = links.join(' / ');
}

function renderEntryRow(dirPath, entry) {
  const full = joinPath(dirPath, entry.name);
  const isDir = entry.type === 'dir';
  const label = isDir ? `${entry.name}/` : entry.name;
  return `
    <tr>
      <td><a href="${linkHrefFor(full, isDir)}">${escapeHtml(label)}</a></td>
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
    const listing = await apiFetch(`/library?path=${encodeURIComponent(dirPath)}`);
    entriesList.innerHTML = listing.entries.length
      ? listing.entries.map((entry) => renderEntryRow(listing.path, entry)).join('')
      : '<tr><td colspan="3">Esta carpeta está vacía.</td></tr>';
  } catch (err) {
    entriesList.innerHTML = `<tr><td colspan="3">${escapeHtml(err.message)}</td></tr>`;
  }
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

async function loadFile(path) {
  directoryView.hidden = true;
  fileView.hidden = false;
  const dir = dirOf(path);
  const name = nameOf(path);
  renderBreadcrumbs(dir);

  document.getElementById('file-name').textContent = name;
  document.getElementById('file-meta').textContent = 'Cargando…';
  const preview = document.getElementById('file-preview');
  preview.innerHTML = '';

  document.getElementById('file-back').onclick = () => {
    window.location.href = dir ? `/library?path=${encodeURIComponent(dir)}` : '/library';
  };
  document.getElementById('file-download').onclick = async () => {
    try {
      const blob = await apiFetchBlob(`/library/file/${encodeFilePath(path)}?download=1`);
      downloadBlob(blob, name);
    } catch (err) {
      showError(err.message);
    }
  };

  const kind = fileKind(name);
  try {
    const blob = await apiFetchBlob(`/library/file/${encodeFilePath(path)}`);
    document.getElementById('file-meta').textContent = formatSize(blob.size);

    if (kind === 'markdown' || kind === 'text') {
      const text = await blob.text();
      if (kind === 'markdown') {
        preview.innerHTML = renderMarkdown(text, (href) => {
          const resolved = resolveRelativePath(dir, href);
          return resolved ? linkHrefFor(resolved.path, resolved.isDir) : href;
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
      // Rendered in a fully sandboxed iframe (no scripts, no same-origin
      // access) since this is still arbitrary HTML — relative links
      // inside it are not rewritten into library navigation the way
      // Markdown's are, since there's no safe way to intercept
      // navigation inside a sandboxed frame without also granting it
      // more privileges than a document view needs.
      const url = URL.createObjectURL(blob);
      preview.innerHTML = `<iframe src="${url}" class="document-frame" sandbox=""></iframe>`;
    } else {
      preview.innerHTML = '<p class="hint">No hay vista previa disponible para este tipo de fichero. Descárgalo para verlo.</p>';
    }
  } catch (err) {
    document.getElementById('file-meta').textContent = '';
    showError(err.message);
  }
}

const params = new URLSearchParams(window.location.search);
const viewPath = params.get('view');
if (viewPath !== null) {
  loadFile(viewPath);
} else {
  loadDirectory(params.get('path') ?? '');
}

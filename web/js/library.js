import {
  apiFetch,
  apiFetchBlob,
  escapeHtml,
  encodeFilePath,
  formatDate,
  showError,
} from './api.js';
import { initPage } from './layout.js';
import { renderMarkdown } from './markdown.js';
import {
  joinPath,
  dirOf,
  nameOf,
  formatSize,
  fileKind,
  resolveRelativePath,
  downloadBlob,
} from './fileBrowser.js';

await initPage();

const directoryView = document.getElementById('directory-view');
const fileView = document.getElementById('file-view');
const entriesList = document.getElementById('entries-list');
const breadcrumbsEl = document.getElementById('breadcrumbs');

function linkHrefFor(path, isDir) {
  return isDir
    ? `/archivo?path=${encodeURIComponent(path)}`
    : `/archivo?view=${encodeURIComponent(path)}`;
}

function renderBreadcrumbs(dirPath) {
  const segments = dirPath ? dirPath.split('/') : [];
  const links = ['<a href="/archivo">Archivo</a>'];
  let acc = '';
  for (const segment of segments) {
    acc = joinPath(acc, segment);
    links.push(`<a href="/archivo?path=${encodeURIComponent(acc)}">${escapeHtml(segment)}</a>`);
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
    window.location.href = dir ? `/archivo?path=${encodeURIComponent(dir)}` : '/archivo';
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
      preview.innerHTML =
        '<p class="hint">No hay vista previa disponible para este tipo de fichero. Descárgalo para verlo.</p>';
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

// Shared helpers for browsing/previewing a document tree over HTTP and
// saving a fetched Blob back to disk — pulled out once instead of
// three near-identical copies: js/archive.js and js/userFiles.js each
// had their own copy of every one of these; js/activity.js (for its
// memoria resources, a different feature entirely, but downloading a
// fetched file and showing its size is the same problem either way)
// duplicated just formatSize (as formatFileSize) and downloadBlob.

export function joinPath(dir, name) {
  return dir ? `${dir}/${name}` : name;
}

export function dirOf(path) {
  const i = path.lastIndexOf('/');
  return i === -1 ? '' : path.slice(0, i);
}

export function nameOf(path) {
  const i = path.lastIndexOf('/');
  return i === -1 ? path : path.slice(i + 1);
}

export function formatSize(bytes) {
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

export function fileKind(name) {
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
export function resolveRelativePath(baseDir, href) {
  if (
    /^([a-z][a-z0-9+.-]*:)?\/\//i.test(href) ||
    href.startsWith('mailto:') ||
    href.startsWith('#')
  ) {
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

// Triggers a browser "save as" for an already-fetched Blob — used
// instead of a plain <a href> wherever the file was fetched via
// apiFetchBlob rather than linked directly. Only archive's and
// userfiles' own file-serving GET routes mirror the session token into
// a cookie too, specifically so a document/file can be a plain,
// browser-navigable link there — everywhere else, a bare link has
// nothing to authenticate it, so this Blob-based download is the only
// option.
export function downloadBlob(blob, filename) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

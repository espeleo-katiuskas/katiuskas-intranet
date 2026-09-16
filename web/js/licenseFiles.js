import { API_BASE, apiFetch, escapeHtml, encodeFilePath } from './api.js';

// Federative license copies live in each person's own "licencias"
// subdirectory of their user files (see internal/userfiles), one file
// per license, named "yyyy-mm-dd-tipo_de_licencia.ext" — the same
// filename-encodes-the-metadata convention dashboard.js's own actas
// already use, so this mirrors that page's loadActas/fileLink shape
// instead of the generic folder browser userFiles.js otherwise gives
// this same directory.

function stripExtension(name) {
  const i = name.lastIndexOf('.');
  return i === -1 ? name : name.slice(0, i);
}

function parseLicenseFileName(name) {
  const match = /^(\d{4})-(\d{2})-(\d{2})-(.+)$/.exec(stripExtension(name));
  if (!match) return null;
  const [, year, month, day, rawType] = match;
  return { year, month, day, rawType };
}

function formatLicenseLabel(name) {
  const parsed = parseLicenseFileName(name);
  if (!parsed) return stripExtension(name);
  const { year, month, day, rawType } = parsed;
  const type = rawType.replace(/_/g, ' ');
  return `${day}/${month}/${year} — ${type.charAt(0).toUpperCase()}${type.slice(1)}`;
}

// The file-serving route (unlike the plain listing one) also accepts
// the session cookie (auth.RequireAuthFromHeaderOrCookie), so a plain
// <a href> works without apiFetchBlob's fetch-then-Blob dance — the
// same reasoning dashboard.js's own fileLink already relies on.
function fileLink(login, name, label) {
  const href = `${API_BASE}/users/${login}/files/file/${encodeFilePath(`licencias/${name}`)}`;
  return `<a href="${href}" target="_blank" rel="noopener">${escapeHtml(label)}</a>`;
}

// initLicenseFiles wires up a fixed set of element ids
// (license-files-*, see profile.html/member.html) to list the given
// login's own "licencias" folder, most recent first. Safe to call on a
// page missing that markup — it simply does nothing.
// hideWhenEmpty mirrors initUserFilesBrowser's own option: profile.html
// hides the whole section when nobody's dropped a license copy in for
// this person yet; member.html leaves it off, so admin/board still see
// the (empty) section and know there's nothing there.
async function initLicenseFiles(login, { hideWhenEmpty = false } = {}) {
  const section = document.getElementById('license-files-section');
  const list = document.getElementById('license-files-list');
  if (!list) return;

  try {
    const listing = await apiFetch(`/users/${login}/files?path=${encodeURIComponent('licencias')}`);
    const files = listing.entries
      .filter((entry) => entry.type === 'file')
      .sort((a, b) => (a.name < b.name ? 1 : a.name > b.name ? -1 : 0));

    if (hideWhenEmpty && section) section.hidden = files.length === 0;
    list.innerHTML = files.length
      ? files.map((f) => `<li>${fileLink(login, f.name, formatLicenseLabel(f.name))}</li>`).join('')
      : '<li>No hay copias de licencias disponibles.</li>';
  } catch {
    // A missing "licencias" folder (nobody's dropped a file in yet) 404s
    // the same as any other unknown subdirectory (see
    // userfiles.Service.ListDirectory) — treated the same as "no
    // files" rather than surfaced as an error.
    if (hideWhenEmpty && section) section.hidden = true;
    list.innerHTML = '<li>No hay copias de licencias disponibles.</li>';
  }
}

export { initLicenseFiles };

import { API_BASE, apiFetch, escapeHtml, encodeFilePath } from './api.js';

// Federative license copies live in each person's own "licencias"
// subdirectory of their user files (see internal/userfiles), one file
// per license, named "yyyy-mm-dd-tipo_de_licencia.ext" — the same
// filename-encodes-the-metadata convention dashboard.js's own actas
// already use, so this mirrors that page's own loadActas/fileLink/year
// <select> shape exactly, instead of the generic folder browser
// userFiles.js otherwise gives this same directory.

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

function licenseYear(name) {
  return parseLicenseFileName(name)?.year ?? null;
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
// (license-files-*, see profile.html/member.html) to browse the given
// login's own "licencias" folder one year at a time, exactly like
// dashboard.js's own loadActas: a <select> built from whatever years
// actually have a license on file (ascending, most recent selected by
// default), and a list showing every one of that year's files, sorted
// by (month, day, tipo_de_licencia) rather than the raw filename
// string. A file whose name doesn't match the yyyy-mm-dd-tipo
// convention has no year to file it under, so — the same as an acta
// named anything else — it's simply left out. Safe to call on a page
// missing that markup — it simply does nothing.
// hideWhenEmpty mirrors initUserFilesBrowser's own option: profile.html
// hides the whole section when nobody's dropped a license copy in for
// this person yet; member.html leaves it off, so admin/board still see
// the (empty) section and know there's nothing there.
async function initLicenseFiles(login, { hideWhenEmpty = false } = {}) {
  const section = document.getElementById('license-files-section');
  const yearSelect = document.getElementById('license-files-year');
  const list = document.getElementById('license-files-list');
  if (!list || !yearSelect) return;

  function showEmpty() {
    yearSelect.innerHTML = '';
    list.innerHTML = '<li>No hay copias de licencias disponibles.</li>';
  }

  try {
    const listing = await apiFetch(`/users/${login}/files?path=${encodeURIComponent('licencias')}`);
    const files = listing.entries.filter((entry) => entry.type === 'file' && licenseYear(entry.name) !== null);
    if (hideWhenEmpty && section) section.hidden = files.length === 0;

    const years = [...new Set(files.map((f) => licenseYear(f.name)))].sort();
    if (!years.length) {
      showEmpty();
      return;
    }

    yearSelect.innerHTML = years.map((year) => `<option value="${year}">${year}</option>`).join('');
    yearSelect.value = years[years.length - 1];

    const renderSelectedYear = () => {
      const yearFiles = files
        .filter((f) => licenseYear(f.name) === yearSelect.value)
        .sort((a, b) => {
          const pa = parseLicenseFileName(a.name);
          const pb = parseLicenseFileName(b.name);
          const keyA = `${pa.month}-${pa.day}-${pa.rawType}`;
          const keyB = `${pb.month}-${pb.day}-${pb.rawType}`;
          return keyA < keyB ? -1 : keyA > keyB ? 1 : 0;
        });
      list.innerHTML = yearFiles.map((f) => `<li>${fileLink(login, f.name, formatLicenseLabel(f.name))}</li>`).join('');
    };
    yearSelect.addEventListener('change', renderSelectedYear);
    renderSelectedYear();
  } catch {
    // A missing "licencias" folder (nobody's dropped a file in yet) 404s
    // the same as any other unknown subdirectory (see
    // userfiles.Service.ListDirectory) — treated the same as "no
    // files" rather than surfaced as an error.
    if (hideWhenEmpty && section) section.hidden = true;
    showEmpty();
  }
}

export { initLicenseFiles };

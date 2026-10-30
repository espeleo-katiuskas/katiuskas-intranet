import { API_BASE, apiFetch, escapeHtml, encodeFilePath } from './api.js';
import { fileKind } from './fileBrowser.js';

// Federative license copies live in each person's own "licencias"
// subdirectory of their user files (see internal/userfiles), one file
// per license, named "yyyy-mm-dd-tipo_de_licencia.ext" — the same
// filename-encodes-the-metadata convention dashboard.js's own actas
// already use, so the year <select>/list-per-year shape here mirrors
// that page's own loadActas exactly. What's shown per license is a
// small clickable thumbnail rather than a text link, though — see
// licenseCard below — so a socio can recognize a license at a glance
// instead of reading a list of dates; clicking it still just opens the
// license's own real file.
//
// A thumbnail, when there is one, lives at
// "licencias/.thumbnails/<full original filename>.jpg" — a dotfile-
// prefixed subdirectory, so it's invisible to the plain "licencias"
// listing this module itself reads (userfiles.Service.ListDirectory
// skips any entry, file or directory, starting with "."), the same
// "invisible companion file, fetched by exact path" trick (the
// library's own .order.json is a dotfile too, though the API reads that
// one itself). Nothing in this
// app generates one — whoever drops a license's own file in is
// expected to drop its thumbnail in alongside it, at 280px on its
// longest side (twice LICENSE_THUMBNAIL_DISPLAY_SIZE in style.css's
// own .license-thumbnail rule) so it still looks sharp on a high-DPI
// screen at the size it's actually displayed.

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
  return `${day}-${month}-${year} — ${type.charAt(0).toUpperCase()}${type.slice(1)}`;
}

function licenseYear(name) {
  return parseLicenseFileName(name)?.year ?? null;
}

// The file-serving route (unlike the plain listing one) also accepts
// the session cookie (auth.RequireAuthFromHeaderOrCookie), so a plain
// <a href>/<img src> works without apiFetchBlob's fetch-then-Blob
// dance — the same reasoning dashboard.js's own fileLink already
// relies on.
function fileHref(login, relPath) {
  return `${API_BASE}/users/${login}/files/file/${encodeFilePath(relPath)}`;
}

// One license's card: a thumbnail (or, failing that, a generic
// placeholder — see wireThumbnailFallbacks) linking to the license's
// own file, with its date/type label underneath. data-fallback-src is
// only set for a license whose own file is itself an image — nothing
// to show a thumbnail's "real" file as a fallback for otherwise, since
// a PDF or any other non-image type can't be dropped straight into an
// <img>.
function licenseCard(login, file) {
  const label = formatLicenseLabel(file.name);
  const originalHref = fileHref(login, `licencias/${file.name}`);
  const thumbHref = fileHref(login, `licencias/.thumbnails/${file.name}.jpg`);
  const fallbackSrc = fileKind(file.name) === 'image' ? originalHref : '';
  return `
    <li class="license-thumbnail-card">
      <a href="${originalHref}" target="_blank" rel="noopener" title="${escapeHtml(label)}">
        <img src="${thumbHref}" alt="${escapeHtml(label)}" class="license-thumbnail" data-fallback-src="${fallbackSrc}">
      </a>
      <span class="license-thumbnail-label">${escapeHtml(label)}</span>
    </li>
  `;
}

// A thumbnail <img> that fails to load (the common case: nobody's
// dropped one in for this license yet) falls back to the license's
// own file if that's itself an image, and — failing that too, or for
// a license that was never an image to begin with — a plain document
// placeholder, rather than the browser's own broken-image icon.
function wireThumbnailFallbacks(list) {
  list.querySelectorAll('img.license-thumbnail').forEach((img) => {
    img.addEventListener('error', function onError() {
      if (img.dataset.fallbackSrc && img.dataset.triedFallback !== '1') {
        img.dataset.triedFallback = '1';
        img.src = img.dataset.fallbackSrc;
        return;
      }
      img.removeEventListener('error', onError);
      const placeholder = document.createElement('span');
      placeholder.className = 'license-thumbnail-placeholder';
      placeholder.textContent = '📄';
      img.replaceWith(placeholder);
    });
  });
}

// initLicenseFiles wires up a fixed set of element ids
// (license-files-*, see perfil.html/socio.html) to browse the given
// login's own "licencias" folder one year at a time, exactly like
// dashboard.js's own loadActas: a <select> built from whatever years
// actually have a license on file (ascending, most recent selected by
// default), and a grid of thumbnail cards for that year's files,
// sorted by (month, day, tipo_de_licencia) rather than the raw
// filename string. A file whose name doesn't match the yyyy-mm-dd-tipo
// convention has no year to file it under, so — the same as an acta
// named anything else — it's simply left out. Safe to call on a page
// missing that markup — it simply does nothing.
// hideWhenEmpty mirrors initUserFilesBrowser's own option: perfil.html
// hides the whole section when nobody's dropped a license copy in for
// this person yet; socio.html leaves it off, so admin/board still see
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
    const files = listing.entries.filter(
      (entry) => entry.type === 'file' && licenseYear(entry.name) !== null,
    );
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
      list.innerHTML = yearFiles.map((f) => licenseCard(login, f)).join('');
      wireThumbnailFallbacks(list);
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

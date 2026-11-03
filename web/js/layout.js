// The sidebar and topbar are identical, static markup on every
// authenticated page — every page but login — that used to be
// copy-pasted by hand into each <page>.html. That's exactly the kind of
// thing that quietly drifts: adding Contabilidad's own nav link meant
// editing every file by hand in one go, and a future icon or wording
// tweak would mean the same again.
// initPage() renders it once from here into the two empty containers
// every page's <body> now has (#sidebar, #topbar) and runs the same
// session bootstrap every page needs — collapsing what used to be five
// near-identical lines at the top of every page's own <page>.js into
// one call. login.html is the one page with no shell and no session
// yet, and never calls this.
import {
  initSession,
  logout,
  applyRoleVisibility,
  initSidebarToggle,
  loadTopbarGreeting,
  currentUser,
  hasAnyRole,
} from './api.js';
import { initRoleOverride } from './roleOverride.js';

// One entry per sidebar link, in display order. roles restricts who
// even sees the link at all (applyRoleVisibility, via data-roles) —
// omitted for a link open to any baseline role (Inicio, Actividades,
// Material, Archivo, Mi perfil all are, same as most of the API itself).
const NAV_ITEMS = [
  {
    href: '/',
    label: 'Inicio',
    icon: '<path d="M4 11.5 12 4l8 7.5"/><path d="M6 10v9.5a.5.5 0 0 0 .5.5H10v-6h4v6h3.5a.5.5 0 0 0 .5-.5V10"/>',
  },
  {
    href: '/socios',
    label: 'Socios',
    roles: ['admin', 'board', 'member'],
    icon: '<circle cx="8.5" cy="7.5" r="3"/><path d="M3 19c0-3.3 2.5-6 5.5-6s5.5 2.7 5.5 6"/><circle cx="16.5" cy="8.5" r="2.3"/><path d="M15 12.2c2.6.4 4.5 2.7 4.5 5.8"/>',
  },
  {
    href: '/actividades',
    label: 'Actividades',
    icon: '<rect x="3.5" y="5" width="17" height="15" rx="2"/><line x1="3.5" y1="9.5" x2="20.5" y2="9.5"/><line x1="8" y1="3" x2="8" y2="7"/><line x1="16" y1="3" x2="16" y2="7"/>',
  },
  {
    href: '/material',
    label: 'Material',
    icon: '<path d="M6 2 L6 17 Q6 19 8 19 L17.5 19 Q19 19 19 17.5 L19 16.5 Q19 14.7 17.2 14.3 L12.5 13.3 L12 13.13 L12 2 Z"/>',
  },
  {
    href: '/repartos',
    label: 'Repartos',
    icon: '<rect x="4" y="9" width="16" height="11" rx="1.5"/><path d="M3.5 6.5h17V9h-17z"/><line x1="12" y1="6.5" x2="12" y2="20"/><path d="M12 6.5C10.5 3.5 7.5 3.5 7.5 5.2c0 1.2 2 1.3 4.5 1.3zM12 6.5c1.5-3 4.5-3 4.5-1.3 0 1.2-2 1.3-4.5 1.3z"/>',
  },
  {
    href: '/contabilidad',
    label: 'Contabilidad',
    roles: ['admin', 'board', 'money'],
    icon: '<circle cx="12" cy="12" r="8.5"/><path d="M14.5 8.5a4 4 0 1 0 0 7"/><line x1="7" y1="10.5" x2="12.5" y2="10.5"/><line x1="7" y1="13.5" x2="12" y2="13.5"/>',
  },
  {
    href: '/presupuestos',
    label: 'Presupuestos',
    icon: '<rect x="4" y="4" width="16" height="16" rx="2"/><line x1="8" y1="16" x2="8" y2="12"/><line x1="12" y1="16" x2="12" y2="8"/><line x1="16" y1="16" x2="16" y2="10.5"/>',
  },
  {
    href: '/archivo',
    label: 'Archivo',
    icon: '<rect x="3.5" y="4" width="17" height="5" rx="1"/><path d="M4.5 9v9a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V9"/><line x1="10" y1="13" x2="14" y2="13"/>',
  },
  {
    href: '/admin',
    label: 'Admin',
    roles: ['admin'],
    icon: '<path d="M12 3.5 5 6v5.5c0 4.2 2.9 7.3 7 9 4.1-1.7 7-4.8 7-9V6z"/><path d="M9 12l2.2 2.2L15.2 10"/>',
  },
  {
    href: '/perfil',
    label: 'Mi perfil',
    icon: '<circle cx="12" cy="8" r="3.5"/><path d="M4.5 20c0-4.1 3.4-7 7.5-7s7.5 2.9 7.5 7"/>',
  },
];

// A page reached *from* a nav item, rather than being one itself,
// still highlights that item as the active one — /actividad (a single
// activity) from /actividades, /socio (a single person) from
// /socios, /cuenta (a single account's own ledger) from
// /contabilidad — so the active link is computed through this alias
// rather than by matching pathname against NAV_ITEMS directly. That
// also means no page has to say which link is "its own" by hand (the
// previous, per-file class="active" was one more thing to remember
// when adding a page).
const ACTIVE_ALIASES = {
  '/actividad': '/actividades',
  '/socio': '/socios',
  '/cuenta': '/contabilidad',
  '/material-prestado': '/material',
  '/justificantes': '/contabilidad',
  '/reparto': '/repartos',
  '/reparto-gestion': '/repartos',
};

function renderNavLink(item, activePath) {
  const activeClass = item.href === activePath ? ' class="active"' : '';
  const rolesAttr = item.roles ? ` data-roles="${item.roles.join(',')}"` : '';
  return `<a href="${item.href}"${activeClass}${rolesAttr}><svg class="nav-icon" viewBox="0 0 24 24" aria-hidden="true">${item.icon}</svg>${item.label}</a>`;
}

const TOPBAR_HTML = `
  <div class="topbar-brand">
    <button id="sidebar-toggle" type="button" class="sidebar-toggle" aria-label="Menú" aria-expanded="false" aria-controls="sidebar">☰</button>
    <img src="/images/logo-katiuskas.png" alt="" class="topbar-logo">
    <h1>Club de Espeleología Katiuskas — Intranet</h1>
  </div>
  <div class="topbar-user">
    <span>Hola, <strong id="topbar-user-name">…</strong></span>
    <button id="role-override-button" type="button" class="link-button" data-roles="admin" title="Ver la intranet como alguien con otros roles">Ver como…</button>
    <button id="logout-button" type="button" class="link-button">Cerrar sesión</button>
  </div>
`;

// Renders synchronously, before initSession()'s own network round trip
// below even starts — there's nothing between the page's initial parse
// and this filling #sidebar/#topbar in for a visitor to see them empty.
function renderShell() {
  const activePath = ACTIVE_ALIASES[window.location.pathname] ?? window.location.pathname;
  document.getElementById('sidebar').innerHTML =
    `<div class="sidebar-header"><img src="/images/logo-katiuskas.png" alt="Club de Espeleología Katiuskas" class="sidebar-logo"><button type="button" id="sidebar-close" class="sidebar-close" aria-label="Cerrar menú">&times;</button></div>` +
    `<nav class="sidebar-nav" id="sidebar-nav">${NAV_ITEMS.map((item) => renderNavLink(item, activePath)).join('')}</nav>`;
  document.getElementById('topbar').innerHTML = TOPBAR_HTML;
}

// The one call every authenticated page's own <page>.js makes before
// anything else. requireAnyRole additionally redirects home if the
// signed-in user holds none of the listed roles — for the couple of
// pages (socio.html, contabilidad.html, admin.html) restricted beyond the ordinary
// baseline check every API route already enforces on its own. Returns
// the signed-in user, same as currentUser(), since almost every caller
// wants it right away anyway.
async function initPage({ requireAnyRole } = {}) {
  renderShell();
  // initSession() itself redirects to /login and returns false on a
  // missing or rejected token — false, not a thrown error, so nothing
  // upstream stopped the previous per-page copy of this same sequence
  // from ploughing on into applyRoleVisibility()/loadTopbarGreeting()
  // regardless, crashing on currentUser() being null (a pre-existing
  // bug in every page, just never noticed since a valid session is the
  // overwhelmingly common case). Returning early here fixes it in the
  // one place that now matters instead of nine.
  if (!(await initSession())) {
    return null;
  }
  applyRoleVisibility();
  document.getElementById('logout-button').addEventListener('click', logout);
  initRoleOverride();
  initSidebarToggle();
  loadTopbarGreeting();

  const user = currentUser();
  if (requireAnyRole && !hasAnyRole(user, requireAnyRole)) {
    window.location.href = '/';
  }
  return user;
}

export { initPage };

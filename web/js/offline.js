// The page's side of working with no connection (the other side is the service worker, /sw.js): it
// registers it, keeps the list of places and its catalogues fresh in the copy that the worker has, says
// when the page is a copy, and forgets all of it when the person signs out.

// The cache where the worker keeps the data: the same name as in sw.js.
const DATA_CACHE = 'katiuskas-data';
const REFRESH_KEY = 'katiuskas:offline-refresh';
// The list of places is kept up to date at most this often, whatever pages are opened.
const REFRESH_EVERY_MS = 6 * 60 * 60 * 1000;
// What the page of the places and the page of one place need besides the places themselves, and the
// holidays that the calendar of the activities marks.
const PRELOAD = [
  '/places',
  '/places/types',
  '/places/subtypes',
  '/places/geologies',
  '/systems',
  '/holidays',
];
// The first page of the activities that are still active, as the page of the activities asks for it (it
// has to be the very same question to be answered from the copy).
const ACTIVE_ACTIVITIES = `/activities?${new URLSearchParams({ limit: '50', offset: '0', order: 'asc', active: 'true' })}`;
// Of how many of them, the nearest, what their pages show is kept ahead of time: they are the ones that
// somebody is about to go to.
const PRELOAD_ACTIVITIES = 10;

function supported() {
  return 'serviceWorker' in navigator && window.isSecureContext;
}

// Waits for the worker to be the one that answers this page's requests: on the very first visit it is
// only installed after the page started, and takes over a moment later.
async function waitForWorker() {
  await navigator.serviceWorker.ready;
  if (navigator.serviceWorker.controller) return true;
  return new Promise((resolve) => {
    const done = (value) => () => resolve(value);
    navigator.serviceWorker.addEventListener('controllerchange', done(true), { once: true });
    setTimeout(done(false), 5000);
  });
}

// Asks for the list of places and what goes with it, through the worker, which keeps the answers. Not more
// than once in a few hours; and nothing if the page itself is a copy.
async function refreshPlaces(apiFetch) {
  try {
    const last = Number(localStorage.getItem(REFRESH_KEY)) || 0;
    if (Date.now() - last < REFRESH_EVERY_MS) return;
    if (!(await waitForWorker())) return;
    const answers = await Promise.all([
      ...PRELOAD.map((path) => apiFetch(path)),
      apiFetch(ACTIVE_ACTIVITIES),
    ]);
    const active = answers[answers.length - 1];
    // What each of the next activities shows, one activity at a time so as not to ask for much at once.
    for (const activity of active.slice(0, PRELOAD_ACTIVITIES)) {
      await Promise.allSettled(
        ['', '/participants', '/places', '/guests', '/resources'].map((part) =>
          apiFetch(`/activities/${activity.id}${part}`),
        ),
      );
    }
    // The files kept by the worker are looked at again too, since a deployment changes them.
    navigator.serviceWorker.controller?.postMessage('refresh-files');
    localStorage.setItem(REFRESH_KEY, String(Date.now()));
  } catch {
    // Without connection, or refused: it is tried again with the next page.
  }
}

// Called by every page once the session is known. `apiFetch` is the one of api.js; `offline` says the
// session could not be checked and the page runs on what is kept.
export function initOffline(apiFetch, { offline }) {
  if (!supported()) return;
  navigator.serviceWorker.register('/sw.js').catch(() => {});
  if (offline) {
    renderOfflineNotice();
  } else {
    // After the page has had its own requests, not before: it is a favour to nobody.
    setTimeout(() => refreshPlaces(apiFetch), 2000);
  }
}

function renderOfflineNotice() {
  const notice = document.createElement('div');
  notice.className = 'offline-notice';
  notice.setAttribute('role', 'status');
  notice.textContent =
    'Sin conexión: se ven los datos guardados en este móvil y no se puede cambiar nada.';
  document.body.prepend(notice);
  // When the connection is back, the page is loaded again, with everything on line.
  window.addEventListener('online', () => window.location.reload(), { once: true });
}

// Forgets the copies of the data (they are of the person who signs out).
export async function clearOfflineData() {
  try {
    localStorage.removeItem(REFRESH_KEY);
    if ('caches' in window) await caches.delete(DATA_CACHE);
  } catch {
    // Nothing to forget, or it cannot be done: the next person to sign in refreshes them anyway.
  }
}

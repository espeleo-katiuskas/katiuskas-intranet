// The page's side of working with no connection (the other side is the service worker, /sw.js): it
// registers it, keeps the list of places and its catalogues fresh in the copy that the worker has, says
// when the page is a copy, sends what the person did with no connection (the outbox, js/outbox.js) when
// there is one, and forgets the copies when the person signs out.
import { flush } from './outbox.js';

// The cache where the worker keeps the data: the same name as in sw.js.
const DATA_CACHE = 'katiuskas-data';
const REFRESH_KEY = 'katiuskas:offline-refresh';
// The list of places is kept up to date at most this often, whatever pages are opened.
const REFRESH_EVERY_MS = 6 * 60 * 60 * 1000;
// What the page of the places and the page of one place need besides the places themselves, the
// holidays that the calendar of the activities marks and the data of interest of the home page.
const PRELOAD = [
  '/places',
  '/places/types',
  '/places/subtypes',
  '/places/geologies',
  '/systems',
  '/holidays',
  '/club-info',
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

// Sends what is waiting in the outbox for this person, and tells `onSent` which entries went.
async function sendOutbox(api, login, onSent) {
  try {
    const sent = await flush(login, api);
    if (sent.length) onSent(sent);
  } catch {
    // Without connection or storage: it is tried again with the next page, or when the connection is back.
  }
}

// Called by every page once the session is known. `api` is { apiFetch, apiFetchForm } of api.js; `offline`
// says the session could not be checked and the page runs on what is kept; `login` is the person and
// `onSent` is called with the entries of the outbox that have just been sent.
export function initOffline(api, { offline, login, onSent }) {
  if (offline) {
    if (supported()) renderOfflineNotice();
  } else {
    // What was left in the outbox goes now, and again whenever the connection comes back while the page is
    // open (a page that was opened off line is loaded again at that moment, and so sends it).
    setTimeout(() => sendOutbox(api, login, onSent), 1500);
    window.addEventListener('online', () => sendOutbox(api, login, onSent));
  }
  if (!supported()) return;
  navigator.serviceWorker.register('/sw.js').catch(() => {});
  // After the page has had its own requests, not before: it is a favour to nobody.
  if (!offline) setTimeout(() => refreshPlaces(api.apiFetch), 2000);
}

function renderOfflineNotice() {
  const notice = document.createElement('div');
  notice.className = 'offline-notice';
  notice.setAttribute('role', 'status');
  notice.textContent =
    'Sin conexión: se ven los datos del móvil. Tus ediciones se envían al volver.';
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

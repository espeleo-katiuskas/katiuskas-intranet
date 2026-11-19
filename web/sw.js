// The service worker of the intranet: it lets some pages work with no connection, which is the usual state
// near a cave. It keeps copies of the pages, scripts and styles, and of the data of the places, and gives them
// when the network does not answer (or is too slow to be any use). Nothing is sent from here: a change made
// with no connection is not kept (see docs/frontend.md, "Working without a connection").
//
// What is kept, and how:
//  * The files of the site (pages, scripts, styles, images): network first, so that on line everything is as
//    fresh as ever; every answer is kept for the next time there is none. At install, the pages in SEED_PAGES
//    and everything they load (found by reading them: scripts, imports, styles) are fetched, so that they
//    work off line even if nobody has opened them yet.
//  * The data (GET of the places, the catalogues of their types, the systems, the activities and the
//    data of interest of the club that the home page shows): network
//    first with a copy of the last answer; the list of places is kept whole, and of the places and of the
//    activities that are opened only the last few (with what the page asks about each) and the last few
//    lists of activities: LIMITS says how many. Nothing else of the API is kept: not the people, not the
//    bytes of a file, not what is written.
// Everything else is left to the browser.

const VERSION = 'v1';
const STATIC_CACHE = `katiuskas-static-${VERSION}`;
// The data is not versioned: it is also deleted from the pages, when the person signs out.
const DATA_CACHE = 'katiuskas-data';

// The pages that must work off line. A page opened on line is kept anyway; these are for the ones that nobody
// has opened yet when the connection goes.
const SEED_PAGES = [
  '/',
  '/login',
  '/lugares',
  '/lugar',
  '/brujula',
  '/actividades',
  '/actividad',
  // Leaflet picks these two by itself, and nothing that is read says so.
  '/vendor/leaflet/images/marker-icon-2x.png',
  '/vendor/leaflet/images/marker-shadow.png',
];

// How long to wait for the network before using the copy there is. Near a cave the connection is often there
// and worth nothing, so waiting for it as long as the browser would is worse than not having it.
const NETWORK_TIMEOUT_MS = 3000;

// How many entries of each kind are kept: the oldest go. The catalogues are kept whole.
const LIMITS = { place: 20, activity: 20, related: 160, list: 12, catalogue: Infinity };

// The GETs of the API that are kept, by path: what the lists and the page of a place and of an activity
// show. Of the paths of the kind `list` the query is part of the question (a page of activities, a year of
// holidays); of the others a query means another question and nothing is kept.
const PLACE = /^\/api\/v1\/places\/\d+$/;
const ACTIVITY = /^\/api\/v1\/activities\/\d+$/;
const RELATED =
  /^\/api\/v1\/(?:places\/\d+\/(?:files|activities)|systems\/\d+(?:\/files)?|activities\/\d+\/(?:participants|places|guests|resources))$/;
const LIST = /^\/api\/v1\/(?:activities|holidays)$/;
const CATALOGUE =
  /^\/api\/v1\/(?:places(?:\/types|\/subtypes|\/geologies)?|systems|club-info)$/;

function dataKind(url) {
  const plain = !url.search;
  if (plain && PLACE.test(url.pathname)) return 'place';
  if (plain && ACTIVITY.test(url.pathname)) return 'activity';
  if (plain && RELATED.test(url.pathname)) return 'related';
  if (LIST.test(url.pathname)) return 'list';
  if (plain && CATALOGUE.test(url.pathname)) return 'catalogue';
  return null;
}

// ----- install: the pages that have to work off line, and what they load -----

const SCRIPT_SRC = /<(?:script|img|link)\b[^>]*?\b(?:src|href)="([^"#]+)"/g;
const JS_PATHS = /(?:from\s*|import\s*\(?\s*)['"]([^'"]+\.js)['"]|['"](\/(?:vendor|images)\/[^'"`]+)['"]/g;
const CSS_URLS = /url\(\s*['"]?(?!data:)([^)'"]+)['"]?\s*\)/g;

function sameOrigin(path, base) {
  const url = new URL(path, base);
  return url.origin === self.location.origin ? url : null;
}

// The files that a file of the site refers to, by reading it: the first-level ones of a page, the imports
// and the images of a script, the images of a style.
async function referencesOf(url, response) {
  const text = await response.clone().text();
  const found = [];
  const patterns = url.pathname.endsWith('.css')
    ? [CSS_URLS]
    : url.pathname.endsWith('.js')
      ? [JS_PATHS]
      : [SCRIPT_SRC];
  for (const pattern of patterns) {
    for (const match of text.matchAll(pattern)) {
      const target = sameOrigin(match[1] ?? match[2], url);
      if (target) found.push(target);
    }
  }
  return found;
}

function staticKey(url) {
  // A page is one file whatever its query: /lugar?id=3 and /lugar?id=4 are the same HTML.
  return isPage(url) ? url.pathname : url.pathname + url.search;
}

function isPage(url) {
  return !/\.[a-z0-9]+$/i.test(url.pathname) && !url.pathname.startsWith('/api/');
}

async function precache() {
  const cache = await caches.open(STATIC_CACHE);
  const queue = SEED_PAGES.map((path) => new URL(path, self.location.origin));
  const seen = new Set();
  while (queue.length) {
    const url = queue.shift();
    const key = staticKey(url);
    if (seen.has(key)) continue;
    seen.add(key);
    try {
      // Asking the server whether it changed (a 304 costs nothing), not the browser's own copy.
      const response = await fetch(url, { cache: 'no-cache' });
      if (!response.ok) continue;
      await cache.put(key, response.clone());
      queue.push(...(await referencesOf(url, response)));
    } catch {
      // The one that fails is fetched when it is used; the others are not worth losing for it.
    }
  }
}

self.addEventListener('install', (event) => {
  event.waitUntil(precache().then(() => self.skipWaiting()));
});

// A page asks for the files to be looked at again from time to time (see js/offline.js): what was kept
// by the install is otherwise only renewed when the file is used on line, which may be never.
self.addEventListener('message', (event) => {
  if (event.data === 'refresh-files') event.waitUntil(precache());
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      for (const name of await caches.keys()) {
        if (name.startsWith('katiuskas-static-') && name !== STATIC_CACHE) await caches.delete(name);
      }
      await self.clients.claim();
    })(),
  );
});

// ----- requests: the network first, with the copy for when it does not answer -----

// Keeps the answer, and the list of the entries of its kind within its limit: the oldest go.
async function remember(cache, key, response, kind) {
  await cache.delete(key); // So that it counts as the latest.
  await cache.put(key, response);
  const limit = LIMITS[kind] ?? Infinity;
  if (limit === Infinity) return;
  const same = (await cache.keys()).filter((request) => {
    const url = new URL(request.url);
    return dataKind(url) === kind;
  });
  for (const request of same.slice(0, Math.max(0, same.length - limit))) {
    await cache.delete(request);
  }
}

async function networkFirst(event, cache, key, kind) {
  const request = event.request;
  const fromNetwork = fetch(request).then(async (response) => {
    // Only a good answer is kept: an error page or a refusal is no use to anybody off line.
    if (response.ok && response.status === 200) {
      await remember(cache, key, response.clone(), kind);
    }
    return response;
  });
  const cached = await cache.match(key, { ignoreVary: true });
  if (!cached) return fromNetwork;
  // There is a copy: it is used if the network fails or takes too long, and the answer, if it comes later,
  // is kept for the next time.
  event.waitUntil(fromNetwork.catch(() => {}));
  return Promise.race([
    fromNetwork.catch(() => cached),
    new Promise((resolve) => setTimeout(() => resolve(cached), NETWORK_TIMEOUT_MS)),
  ]);
}

self.addEventListener('fetch', (event) => {
  const request = event.request;
  if (request.method !== 'GET') return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;

  if (url.pathname.startsWith('/api/')) {
    const kind = dataKind(url);
    if (!kind) return;
    event.respondWith(
      caches
        .open(DATA_CACHE)
        .then((cache) => networkFirst(event, cache, url.pathname + url.search, kind)),
    );
    return;
  }
  // The file of a service worker itself is the browser's business.
  if (url.pathname === '/sw.js') return;
  // A range (a video, a long PDF) is not for a cache of pages.
  if (request.headers.has('range')) return;

  event.respondWith(
    caches
      .open(STATIC_CACHE)
      .then((cache) => networkFirst(event, cache, staticKey(url), 'static'))
      .catch(
        () =>
          new Response('Sin conexión y sin copia de esta página.', {
            status: 503,
            headers: { 'Content-Type': 'text/plain; charset=utf-8' },
          }),
      ),
  );
});

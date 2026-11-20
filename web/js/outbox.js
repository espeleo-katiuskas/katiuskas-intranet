// The outbox: what a person did with no connection and has to be sent when there is one. Today it is the
// entries that are added to the memoria of an activity (a text, or a file such as a photo), the changes
// to a place, the points added to one and the photos and files of one; the place of an entry is `kind`, so what comes later (the
// photos of a place) goes in the same queue. It lives in IndexedDB, which keeps a file as it is (localStorage cannot), and an entry
// stays there until it is sent or the person discards it: closing the page, the browser or the phone
// loses nothing.
//
// An entry is { kind, login, queuedAt, …what that kind needs, error? }: `login` is who made it (it is only
// sent as that person, never as whoever signs in next on that phone) and `error`, if there is one, is why
// the server refused it, which has to be looked at by the person (retried or discarded).

import { describeFields, placeBody, sameValue } from './place-body.js';

const DB_NAME = 'katiuskas-offline';
const STORE = 'outbox';

function openDatabase() {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, 1);
    request.onupgradeneeded = () => {
      request.result.createObjectStore(STORE, { keyPath: 'id', autoIncrement: true });
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

// One transaction on the store: `action` gets the store and returns the request whose result is wanted.
async function run(mode, action) {
  const db = await openDatabase();
  try {
    return await new Promise((resolve, reject) => {
      const transaction = db.transaction(STORE, mode);
      const request = action(transaction.objectStore(STORE));
      transaction.oncomplete = () => resolve(request?.result);
      transaction.onerror = () => reject(transaction.error);
      transaction.onabort = () => reject(transaction.error);
    });
  } finally {
    db.close();
  }
}

// Adds an entry and says its id. The first one also asks the browser not to throw the outbox away to make
// room, which it may do with what a site keeps if it has not been asked.
export async function enqueue(entry) {
  navigator.storage?.persist?.().catch(() => {});
  return run('readwrite', (store) =>
    store.add({ ...entry, queuedAt: entry.queuedAt ?? Date.now() }),
  );
}

// The entries, the oldest first; with `where`, only those it accepts.
export async function entries(where = () => true) {
  const all = await run('readonly', (store) => store.getAll());
  return (all ?? []).filter(where).sort((a, b) => a.id - b.id);
}

export function discard(id) {
  return run('readwrite', (store) => store.delete(id));
}

// Changes some fields of an entry (the reason it was refused, or none to try it again).
export async function update(id, changes) {
  const db = await openDatabase();
  try {
    await new Promise((resolve, reject) => {
      const transaction = db.transaction(STORE, 'readwrite');
      const store = transaction.objectStore(STORE);
      const request = store.get(id);
      request.onsuccess = () => {
        if (request.result) store.put({ ...request.result, ...changes });
      };
      transaction.oncomplete = resolve;
      transaction.onerror = () => reject(transaction.error);
      transaction.onabort = () => reject(transaction.error);
    });
  } finally {
    db.close();
  }
}

// What an entry is sent with. `send` of a kind throws what apiFetch throws.
const SENDERS = {
  memoria: async (entry, { apiFetchForm }) => {
    const form = new FormData();
    form.set('type', entry.type);
    form.set('title', entry.title);
    if (entry.placeId) form.set('place_id', String(entry.placeId));
    if (entry.type === 'text') form.set('description', entry.description);
    else form.set('file', entry.file, entry.fileName);
    await apiFetchForm(`/activities/${entry.activityId}/resources`, form);
  },
  // The edit of a place names only the fields the person changed (PATCH), so what others changed
  // meanwhile in other fields is not touched. If somebody changed one of these fields too (it is neither
  // what the person saw nor what they want) it is not sent: the person decides, with the place in front.
  'place-edit': async (entry, { apiFetch }) => {
    const fresh = placeBody(await apiFetch(`/places/${entry.placeId}`));
    const fields = Object.keys(entry.changes);
    if (fields.every((f) => sameValue(fresh[f], entry.changes[f]))) return; // Already there.
    const clash = fields.filter(
      (f) => !sameValue(fresh[f], entry.base[f]) && !sameValue(fresh[f], entry.changes[f]),
    );
    if (clash.length) {
      throw new Error(
        `El lugar ha cambiado mientras tanto (${describeFields(clash)}). Mira cómo está ahora y, si hace falta, vuelve a hacer el cambio.`,
      );
    }
    await apiFetch(`/places/${entry.placeId}`, {
      method: 'PATCH',
      body: JSON.stringify(entry.changes),
    });
  },
  'place-file': async (entry, { apiFetchForm }) => {
    const form = new FormData();
    form.set('file', entry.file, entry.fileName);
    if (entry.title) form.set('title', entry.title);
    await apiFetchForm(`/places/${entry.placeId}/files`, form);
  },
  'place-point': async (entry, { apiFetch }) => {
    await apiFetch(`/places/${entry.placeId}/points`, {
      method: 'POST',
      body: JSON.stringify(entry.point),
    });
  },
};

// Whether the entry is already there: a request that failed for want of a connection may have reached the
// server all the same (the answer is what was lost), and sending it again would put it twice. The memoria
// of the activity says: the same title and kind (and text), by the same person, made after the entry.
const ALREADY_SENT = {
  memoria: async (entry, { apiFetch }) => {
    const resources = await apiFetch(`/activities/${entry.activityId}/resources`);
    return resources.some(
      (r) =>
        r.author_login === entry.login &&
        r.type === entry.type &&
        r.title === entry.title &&
        (entry.type !== 'text' || r.description === entry.description) &&
        Date.parse(r.created_at) >= entry.queuedAt - 5 * 60 * 1000,
    );
  },
  'place-file': async (entry, { apiFetch }) => {
    const files = await apiFetch(`/places/${entry.placeId}/files`);
    return files.some(
      (f) =>
        f.uploaded_by === entry.login &&
        f.file_name === entry.fileName &&
        f.file_size === entry.file.size &&
        Date.parse(f.created_at) >= entry.queuedAt - 5 * 60 * 1000,
    );
  },
  'place-point': async (entry, { apiFetch }) => {
    const { points } = await apiFetch(`/places/${entry.placeId}`);
    const p = entry.point;
    return (points ?? []).some(
      (x) =>
        x.kind === p.kind &&
        (x.name ?? null) === (p.name ?? null) &&
        (x.note ?? null) === (p.note ?? null) &&
        sameValue(x.lat, p.lat) &&
        sameValue(x.lon, p.lon),
    );
  },
};

// Sends what is in the outbox of this person, in order. It stops at the first request that finds no
// connection (the rest wait for the next time) and, for one that the server refuses, marks it with the
// reason and goes on with the others. It says which entries were sent. Only one page does it at a time.
// `api` is { apiFetch, apiFetchForm } of api.js (this module does not import it, so that api.js can import
// the module that uses this one).
export async function flush(login, api) {
  const work = async () => {
    const sent = [];
    for (const entry of await entries((e) => e.login === login && !e.error)) {
      try {
        if (!(await ALREADY_SENT[entry.kind]?.(entry, api))) await SENDERS[entry.kind](entry, api);
        await discard(entry.id);
        sent.push(entry);
      } catch (err) {
        if (err.network || err.status === 401) break;
        await update(entry.id, { error: err.message });
      }
    }
    return sent;
  };
  return navigator.locks ? navigator.locks.request('katiuskas-outbox', work) : work();
}

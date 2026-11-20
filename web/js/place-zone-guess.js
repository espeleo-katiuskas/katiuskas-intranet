import { distanceM, formatDistance } from './bearing.js';

// A new place made with no connection cannot ask which zone its coordinates are in (the API does, with
// the help of an outside service). The list of places is always on the phone, each place with its
// coordinates and its zone, so the zone of the nearest one is a good first guess: the same municipality or
// province in most cases, though not always at a border. It is only a guess, and is replaced by what the
// API says when the place is sent.

// Farther than this the nearest place says nothing about the zone.
export const MAX_GUESS_M = 5000;

// The nearest of `places` (items of the list of places) to the point, within MAX_GUESS_M, with how far it is;
// or null.
export function nearestPlace(lat, lon, places) {
  let best = null;
  for (const place of places ?? []) {
    const metres = distanceM(lat, lon, place.lat, place.lon);
    if (metres <= MAX_GUESS_M && (!best || metres < best.metres)) best = { place, metres };
  }
  return best;
}

// What the form says about the zone of a new place made off line, for a point.
export function guessText(lat, lon, places) {
  const near = nearestPlace(lat, lon, places);
  if (!near) {
    return 'La zona se buscará al enviarlo. Si no se encuentra, tendrás que elegirla.';
  }
  return `Zona propuesta: ${near.place.where} (la de «${near.place.name}», a ${formatDistance(near.metres)}). Se comprobará al enviarlo.`;
}

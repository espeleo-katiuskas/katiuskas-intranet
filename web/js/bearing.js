// The maths of finding a point from where one is: how far it is, which way, and which way the phone
// points. Longitudes and latitudes are WGS84 degrees and every angle is in degrees, clockwise from north.

const EARTH_RADIUS_M = 6371008.8;
const RAD = Math.PI / 180;

// The distance in metres between two points on the Earth (haversine).
export function distanceM(lat1, lon1, lat2, lon2) {
  const dLat = (lat2 - lat1) * RAD;
  const dLon = (lon2 - lon1) * RAD;
  const a =
    Math.sin(dLat / 2) ** 2 + Math.cos(lat1 * RAD) * Math.cos(lat2 * RAD) * Math.sin(dLon / 2) ** 2;
  return 2 * EARTH_RADIUS_M * Math.asin(Math.sqrt(a));
}

// The direction to walk from the first point to reach the second (0 to 360, 0 is north, 90 east).
export function bearingDeg(lat1, lon1, lat2, lon2) {
  const dLon = (lon2 - lon1) * RAD;
  const y = Math.sin(dLon) * Math.cos(lat2 * RAD);
  const x =
    Math.cos(lat1 * RAD) * Math.sin(lat2 * RAD) -
    Math.sin(lat1 * RAD) * Math.cos(lat2 * RAD) * Math.cos(dLon);
  return (Math.atan2(y, x) / RAD + 360) % 360;
}

// How far round, from -180 to 180, one has to turn from the angle `from` to get to the angle `to`
// (positive is clockwise).
export function angleDiff(to, from) {
  return ((((to - from) % 360) + 540) % 360) - 180;
}

// The direction a phone points, from what the browser says of its orientation (`alpha`, `beta` and
// `gamma` of an absolute `DeviceOrientationEvent`, where alpha counts anticlockwise from north): that of
// its top edge when it lies flat, like a compass, and that of its back when it is held upright, which is
// how a phone is held to find a way (the formula of the W3C specification, which has no answer for a
// phone that lies flat). Between the two, the tilt at which it changes is 45 degrees.
export function deviceHeading(alpha, beta, gamma) {
  if (Math.abs(beta ?? 0) < 45) return (360 - (alpha ?? 0)) % 360;
  const x = (beta ?? 0) * RAD;
  const y = (gamma ?? 0) * RAD;
  const z = (alpha ?? 0) * RAD;
  const vx = -Math.cos(z) * Math.sin(y) - Math.sin(z) * Math.sin(x) * Math.cos(y);
  const vy = -Math.sin(z) * Math.sin(y) + Math.cos(z) * Math.sin(x) * Math.cos(y);
  let heading = Math.atan(vx / vy);
  if (vy < 0) heading += Math.PI;
  else if (vx < 0) heading += 2 * Math.PI;
  return (heading / RAD) % 360;
}

const POINTS = ['N', 'NE', 'E', 'SE', 'S', 'SO', 'O', 'NO'];

// The nearest of the eight points of the compass, in Spanish ("SO" is southwest).
export function cardinal(degrees) {
  return POINTS[Math.round((((degrees % 360) + 360) % 360) / 45) % 8];
}

// A distance as people say it: "340 m" below a kilometre, "2,3 km" up to ten, "35 km" beyond.
export function formatDistance(metres) {
  if (metres < 1000) return `${Math.round(metres)} m`;
  const km = metres / 1000;
  return `${km < 10 ? km.toFixed(1).replace('.', ',') : Math.round(km)} km`;
}

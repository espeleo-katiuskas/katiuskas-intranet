// Coordinates of the places: they are stored as WGS84 latitude and longitude in degrees, and a person
// may write them as decimals ("40.4168, -3.7038") or as UTM ("30T 440290 4474260"), which is what is
// usual in Spain. This reads either, writes both, and converts between UTM and latitude and
// longitude. ETRS89, the datum of the Spanish UTM, differs from WGS84 by centimetres, which is
// ignored. The conversion is Krüger's series to the sixth order in the third flattening (Karney,
// 2011), exact to far less than a millimetre inside a zone.

const A = 6378137;
const F = 1 / 298.257223563;
const K0 = 0.9996;
const N = F / (2 - F);

const n2 = N * N;
const n3 = n2 * N;
const n4 = n3 * N;
const n5 = n4 * N;
const n6 = n5 * N;

// The rectifying radius.
const RADIUS = (A / (1 + N)) * (1 + n2 / 4 + n4 / 64 + n6 / 256);

const ALPHA = [
  N / 2 - (2 * n2) / 3 + (5 * n3) / 16 + (41 * n4) / 180 - (127 * n5) / 288 + (7891 * n6) / 37800,
  (13 * n2) / 48 - (3 * n3) / 5 + (557 * n4) / 1440 + (281 * n5) / 630 - (1983433 * n6) / 1935360,
  (61 * n3) / 240 - (103 * n4) / 140 + (15061 * n5) / 26880 + (167603 * n6) / 181440,
  (49561 * n4) / 161280 - (179 * n5) / 168 + (6601661 * n6) / 7257600,
  (34729 * n5) / 80640 - (3418889 * n6) / 1995840,
  (212378941 * n6) / 319334400,
];

const BETA = [
  N / 2 - (2 * n2) / 3 + (37 * n3) / 96 - n4 / 360 - (81 * n5) / 512 + (96199 * n6) / 604800,
  n2 / 48 + n3 / 15 - (437 * n4) / 1440 + (46 * n5) / 105 - (1118711 * n6) / 3870720,
  (17 * n3) / 480 - (37 * n4) / 840 - (209 * n5) / 4480 + (5569 * n6) / 90720,
  (4397 * n4) / 161280 - (11 * n5) / 504 - (830251 * n6) / 7257600,
  (4583 * n5) / 161280 - (108847 * n6) / 3991680,
  (20648693 * n6) / 638668800,
];

const DELTA = [
  2 * N - (2 * n2) / 3 - 2 * n3 + (116 * n4) / 45 + (26 * n5) / 45 - (2854 * n6) / 675,
  (7 * n2) / 3 - (8 * n3) / 5 - (227 * n4) / 45 + (2704 * n5) / 315 + (2323 * n6) / 945,
  (56 * n3) / 15 - (136 * n4) / 35 - (1262 * n5) / 105 + (73814 * n6) / 2835,
  (4279 * n4) / 630 - (332 * n5) / 35 - (399572 * n6) / 14175,
  (4174 * n5) / 315 - (144838 * n6) / 6237,
  (601676 * n6) / 22275,
];

const RAD = Math.PI / 180;
const BANDS = 'CDEFGHJKLMNPQRSTUVWX';

// The central meridian of a zone, in degrees.
function centralMeridian(zone) {
  return zone * 6 - 183;
}

// The zone of a longitude (1 to 60).
function zoneOf(lon) {
  return Math.min(60, Math.max(1, Math.floor((lon + 180) / 6) + 1));
}

// The latitude band letter (C to X, without I and O) of a latitude, or null outside 80°S–84°N.
function bandOf(lat) {
  if (lat < -80 || lat > 84) return null;
  return BANDS[Math.min(19, Math.floor((lat + 80) / 8))];
}

// Latitude and longitude in degrees → UTM in the zone of the longitude, or the one asked for.
function toUtm(lat, lon, zone = zoneOf(lon)) {
  const phi = lat * RAD;
  const dLambda = (lon - centralMeridian(zone)) * RAD;
  const c = (2 * Math.sqrt(N)) / (1 + N);
  const sinPhi = Math.sin(phi);
  const t = Math.sinh(Math.atanh(sinPhi) - c * Math.atanh(c * sinPhi));
  const xiP = Math.atan2(t, Math.cos(dLambda));
  const etaP = Math.atanh(Math.sin(dLambda) / Math.sqrt(1 + t * t));
  let xi = xiP;
  let eta = etaP;
  ALPHA.forEach((alpha, i) => {
    const j = 2 * (i + 1);
    xi += alpha * Math.sin(j * xiP) * Math.cosh(j * etaP);
    eta += alpha * Math.cos(j * xiP) * Math.sinh(j * etaP);
  });
  const south = lat < 0;
  return {
    zone,
    band: bandOf(lat),
    south,
    easting: 500000 + K0 * RADIUS * eta,
    northing: (south ? 10000000 : 0) + K0 * RADIUS * xi,
  };
}

// UTM → latitude and longitude in degrees.
function fromUtm({ zone, south = false, easting, northing }) {
  const xi = (northing - (south ? 10000000 : 0)) / (K0 * RADIUS);
  const eta = (easting - 500000) / (K0 * RADIUS);
  let xiP = xi;
  let etaP = eta;
  BETA.forEach((beta, i) => {
    const j = 2 * (i + 1);
    xiP -= beta * Math.sin(j * xi) * Math.cosh(j * eta);
    etaP -= beta * Math.cos(j * xi) * Math.sinh(j * eta);
  });
  const chi = Math.asin(Math.sin(xiP) / Math.cosh(etaP));
  let phi = chi;
  DELTA.forEach((delta, i) => {
    phi += delta * Math.sin(2 * (i + 1) * chi);
  });
  const lambda = centralMeridian(zone) * RAD + Math.atan2(Math.sinh(etaP), Math.cos(xiP));
  return { lat: phi / RAD, lon: lambda / RAD };
}

function inRange(lat, lon) {
  return (
    Number.isFinite(lat) && Number.isFinite(lon) && Math.abs(lat) <= 90 && Math.abs(lon) <= 180
  );
}

function number(text) {
  return Number(text.replace(',', '.'));
}

// A UTM reading: the zone with its band letter ("30T"), then the easting and the northing, in metres
// ("30T 440290 4474260", also "UTM 30 T 440290.5 4474260.5" or "huso 30 X 440290 Y 4474260"). Without a
// zone, the 30 is assumed, which is where Madrid and most of Spain are: the page shows what it
// understood, so a wrong guess is seen.
const UTM_WITH_ZONE =
  /(\d{1,2})\s*([C-HJ-NP-X])?[\s,;]+(?:x\s*)?(\d{5,7}(?:[.,]\d+)?)[\s,;]+(?:y\s*)?(\d{6,8}(?:[.,]\d+)?)/i;
const UTM_WITHOUT_ZONE =
  /^\s*(?:utm\s*)?(?:x\s*)?(\d{5,7}(?:[.,]\d+)?)[\s,;]+(?:y\s*)?(\d{6,8}(?:[.,]\d+)?)\s*$/i;

function parseUtm(text) {
  const cleaned = text.replace(/\b(?:utm|huso|zona|zone)\b/gi, ' ');
  let match = UTM_WITH_ZONE.exec(cleaned);
  let assumedZone = false;
  let zone;
  let band;
  let easting;
  let northing;
  if (match) {
    zone = Number(match[1]);
    band = match[2]?.toUpperCase();
    easting = number(match[3]);
    northing = number(match[4]);
  } else {
    match = UTM_WITHOUT_ZONE.exec(cleaned);
    if (!match) return null;
    zone = 30;
    assumedZone = true;
    easting = number(match[1]);
    northing = number(match[2]);
  }
  if (zone < 1 || zone > 60 || easting < 100000 || easting > 900000 || northing > 10000000)
    return null;
  // Bands C to M are the southern hemisphere.
  const south = band ? BANDS.indexOf(band) < BANDS.indexOf('N') : false;
  const { lat, lon } = fromUtm({ zone, south, easting, northing });
  if (!inRange(lat, lon)) return null;
  return { lat, lon, source: 'utm', zone, band: band ?? null, assumedZone };
}

// Degrees as decimals, with a comma or a space between them, and a point or a comma as the decimal
// separator ("40.4168, -3.7038", "40,4168 -3,7038"); with a hemisphere letter after each, N and S, E
// and W (or O, oeste), that makes them negative when they are S or W.
function parseDecimal(text) {
  const found = [...text.matchAll(/(-?\d+(?:[.,]\d+)?)\s*°?\s*([NSEWO])?/gi)];
  if (found.length !== 2) return null;
  const read = (m) => {
    const value = number(m[1]);
    return m[2] && 'SWO'.includes(m[2].toUpperCase()) ? -Math.abs(value) : value;
  };
  const lat = read(found[0]);
  const lon = read(found[1]);
  if (!inRange(lat, lon)) return null;
  return { lat, lon, source: 'decimal' };
}

// What a person wrote: { lat, lon, source, … } or null if it is not coordinates. UTM is tried first,
// since its numbers are far too big to be degrees.
function parseCoordinates(text) {
  const trimmed = String(text ?? '').trim();
  if (!trimmed) return null;
  return parseUtm(trimmed) ?? parseDecimal(trimmed);
}

// "40.41680, -3.70380": about a metre of precision.
function formatDecimal(lat, lon) {
  return `${lat.toFixed(5)}, ${lon.toFixed(5)}`;
}

// "30T 440290 4474260", or an empty text where UTM does not reach (the poles).
function formatUtm(lat, lon) {
  const utm = toUtm(lat, lon);
  if (!utm.band) return '';
  return `${utm.zone}${utm.band} ${Math.round(utm.easting)} ${Math.round(utm.northing)}`;
}

// A link that shows the place on a map, in Google Maps.
function googleMapsUrl(lat, lon) {
  return `https://www.google.com/maps/search/?api=1&query=${lat},${lon}`;
}

export { formatDecimal, formatUtm, fromUtm, googleMapsUrl, parseCoordinates, toUtm, zoneOf };

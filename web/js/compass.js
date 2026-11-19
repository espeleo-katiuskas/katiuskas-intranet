import {
  angleDiff,
  bearingDeg,
  cardinal,
  deviceHeading,
  distanceM,
  formatDistance,
} from './bearing.js';
import { formatDecimal } from './coordinates.js';
import { initPage } from './layout.js';

await initPage();

// The page that says how far a point is and which way: "/brujula?lat=…&lon=…&nombre=…&lugar=<id>". It
// watches the GPS for where the person is, and for which way the phone points it takes the compass, if the
// browser gives it, and else the direction the GPS says the person is moving in (assuming the phone is
// pointed where they walk). A circle shows two things, turned so that its top is the way the phone
// points: where the north is and where the point is.

const params = new URLSearchParams(window.location.search);
const target = { lat: Number(params.get('lat')), lon: Number(params.get('lon')) };
const name = (params.get('nombre') ?? '').trim();
const placeId = Number(params.get('lugar')) || null;

const message = document.getElementById('compass-message');
const panel = document.getElementById('compass-panel');
const distanceLine = document.getElementById('compass-distance');
const directionLine = document.getElementById('compass-direction');
const sourceLine = document.getElementById('compass-source');
const accuracyLine = document.getElementById('compass-accuracy');
const enableButton = document.getElementById('compass-enable');
const northMark = document.getElementById('compass-north-mark');
const northLetter = document.getElementById('compass-north-letter');
const targetMark = document.getElementById('compass-target-mark');

if (placeId) {
  const back = document.getElementById('back-link');
  back.href = `/lugar?id=${placeId}`;
  back.textContent = '← Volver al lugar';
}

// How long a compass reading or a direction of movement is still the one to trust.
const COMPASS_FRESH_MS = 2500;
const GPS_HEADING_FRESH_MS = 8000;
// Below this speed (m/s) the direction the GPS gives is noise, not where the person goes.
const MIN_MOVING_SPEED = 0.7;
// A person this close, or closer than the GPS can tell, is there.
const ARRIVED_M = 15;
const SMOOTHING = 0.25;
// How far from the centre of the circle the letter N is drawn.
const LETTER_RADIUS = 52;
// How long without a reading of the compass before the button that asks for it is shown.
const COMPASS_ASK_AFTER_MS = 1500;

const GEOLOCATION_ERRORS = {
  1: 'No se ha dado permiso para usar la ubicación. Actívalo en los ajustes del navegador y vuelve a abrir esta página.',
  2: 'No se ha podido saber dónde estás.',
  3: 'Se tarda demasiado en saber dónde estás. Sal a un sitio abierto.',
};

function say(text) {
  message.textContent = text;
  message.hidden = false;
}

const valid =
  Number.isFinite(target.lat) &&
  Number.isFinite(target.lon) &&
  Math.abs(target.lat) <= 90 &&
  Math.abs(target.lon) <= 180 &&
  params.get('lat') !== null;

const state = {
  position: null, // { lat, lon, accuracy }
  compass: null, // The smoothed heading of the compass, and when it last came.
  compassAt: 0,
  gps: null, // The last direction of movement that the GPS gave, and when.
  gpsAt: 0,
};

// The way the phone points, and where that comes from: the compass, else the movement, else nobody knows.
function heading(now) {
  if (state.compass !== null && now - state.compassAt < COMPASS_FRESH_MS) {
    return { degrees: state.compass, source: 'compass' };
  }
  if (state.gps !== null && now - state.gpsAt < GPS_HEADING_FRESH_MS) {
    return { degrees: state.gps, source: 'movement' };
  }
  return { degrees: 0, source: 'none' };
}

const SOURCE_TEXT = {
  compass: 'Dirección: la brújula del móvil. El círculo gira con él: arriba es hacia donde apunta.',
  movement:
    'Dirección: la de tu movimiento, como si el móvil apuntara hacia donde caminas. Si te paras, se queda la última.',
  none: 'Sin dirección todavía: camina unos pasos y la tomaré de tu movimiento. Mientras, el norte está arriba.',
};

let frame = 0;
function render() {
  frame = 0;
  if (!state.position) return;
  const { lat, lon, accuracy } = state.position;
  const distance = distanceM(lat, lon, target.lat, target.lon);
  const bearing = bearingDeg(lat, lon, target.lat, target.lon);
  const { degrees, source } = heading(performance.now());

  distanceLine.textContent = formatDistance(distance);
  // The circle is turned so that its top is the way the phone points: the north is where it is minus
  // that, and so is the point.
  northMark.setAttribute('transform', `rotate(${-degrees})`);
  // The letter goes with the mark but stays upright, so it is placed and not turned.
  const northAngle = (-degrees * Math.PI) / 180;
  northLetter.setAttribute('x', (LETTER_RADIUS * Math.sin(northAngle)).toFixed(1));
  northLetter.setAttribute('y', (-LETTER_RADIUS * Math.cos(northAngle)).toFixed(1));
  targetMark.setAttribute('transform', `rotate(${bearing - degrees})`);

  const arrived = distance <= Math.max(ARRIVED_M, accuracy);
  directionLine.textContent =
    `${name ? `${name}: ` : ''}${arrived ? 'ya estás aquí, o muy cerca; ' : ''}` +
    `está al ${cardinal(bearing)} (${Math.round(bearing)}°).`;
  sourceLine.textContent = SOURCE_TEXT[source];
  accuracyLine.textContent = `Precisión del GPS: unos ${Math.round(accuracy)} m.`;
}

function schedule() {
  if (!frame) frame = requestAnimationFrame(render);
}

function onPosition({ coords }) {
  state.position = { lat: coords.latitude, lon: coords.longitude, accuracy: coords.accuracy };
  if (Number.isFinite(coords.heading) && (coords.speed ?? 0) > MIN_MOVING_SPEED) {
    state.gps = coords.heading;
    state.gpsAt = performance.now();
  }
  message.hidden = true;
  panel.hidden = false;
  schedule();
}

function onPositionError(error) {
  // A failure while there already is a position (a tunnel, a cave mouth) leaves what was last known.
  if (!state.position) say(GEOLOCATION_ERRORS[error.code] ?? GEOLOCATION_ERRORS[2]);
}

// What the compass says, from the two events that carry it: `deviceorientationabsolute` (Chrome on
// Android: angles with the north as reference) and `deviceorientation` with `webkitCompassHeading` (Safari
// on iPhone). A plain `deviceorientation` has angles from an arbitrary start and is no use.
function onOrientation(event) {
  let degrees = null;
  if (typeof event.webkitCompassHeading === 'number') {
    degrees = event.webkitCompassHeading;
  } else if (
    (event.absolute === true || event.type === 'deviceorientationabsolute') &&
    event.alpha !== null
  ) {
    degrees = deviceHeading(event.alpha, event.beta, event.gamma);
  }
  if (degrees === null || Number.isNaN(degrees)) return;
  // The reading shakes: each one moves the heading a part of the way, round the short side of the circle.
  state.compass =
    state.compass === null
      ? degrees
      : (state.compass + SMOOTHING * angleDiff(degrees, state.compass) + 360) % 360;
  state.compassAt = performance.now();
  schedule();
}

function listenToCompass() {
  window.addEventListener('deviceorientationabsolute', onOrientation, true);
  window.addEventListener('deviceorientation', onOrientation, true);
}

// The compass is listened to at once. Safari (and the Chrome that has followed it) asks for permission to
// it, and only from a tap: if the function that asks exists and no reading has come in a moment, a button
// offers to ask. The listeners are already there when the permission is given.
function setUpCompass() {
  if (typeof DeviceOrientationEvent === 'undefined') return;
  listenToCompass();
  if (typeof DeviceOrientationEvent.requestPermission !== 'function') return;
  setTimeout(() => {
    if (state.compass !== null) return;
    enableButton.hidden = false;
    enableButton.addEventListener('click', async () => {
      try {
        if ((await DeviceOrientationEvent.requestPermission()) === 'granted') {
          enableButton.hidden = true;
        }
      } catch {
        enableButton.hidden = true;
      }
    });
  }, COMPASS_ASK_AFTER_MS);
}

if (!valid) {
  document.getElementById('compass-title').textContent = 'Ir a un punto';
  say(
    'Falta el punto al que ir. Abre esta página desde el botón «Rumbo» de un lugar.',
  );
} else {
  document.getElementById('compass-title').textContent = name ? `Hacia ${name}` : 'Ir a un punto';
  document.title = `${name ? `Hacia ${name}` : 'Ir a un punto'} — Club de Espeleología Katiuskas`;
  document.getElementById('compass-destination').textContent =
    `Coordenadas del destino: ${formatDecimal(target.lat, target.lon)}.`;
  if (!navigator.geolocation || !window.isSecureContext) {
    say('Este navegador no puede decir dónde estás (hace falta GPS y una conexión segura).');
  } else {
    say('Buscando tu posición…');
    navigator.geolocation.watchPosition(onPosition, onPositionError, {
      enableHighAccuracy: true,
      maximumAge: 1000,
      timeout: 20000,
    });
    setUpCompass();
    // The compass and the movement go stale with time, and the page has to notice it by itself.
    setInterval(schedule, 1000);
  }
}

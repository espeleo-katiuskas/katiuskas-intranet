// What /repartos, /reparto, /reparto-pedido and
// /reparto-recogida share: the labels of a distribution's states, how its
// form is read and filled, how choices and people are shown, and the
// searchable people picker. The server (internal/distributions) is the
// authority on every rule; nothing here decides who may do what beyond hiding
// controls.
import { apiFetch, escapeHtml, formatDate, pickerLabel, resolvePickerId } from './api.js';

const distributionStatusLabels = {
  draft: 'Borrador',
  open: 'Abierto',
  closed: 'Cerrado',
  delivering: 'En entrega',
  done: 'Terminado',
};

// What each state means, for a tooltip on its badge.
const distributionStatusHints = {
  draft: 'Se puede editar. Los socios todavía no lo ven.',
  open: 'Los socios pueden hacer su pedido y elegir las opciones.',
  closed: 'Ya no se puede cambiar nada. Todavía no se puede entregar.',
  delivering: 'Se pueden ir entregando los pedidos.',
  done: 'El reparto ha terminado.',
};

const STATUS_ORDER = ['draft', 'open', 'closed', 'delivering', 'done'];

const audienceLabels = {
  all_members: 'Todos los socios',
  signup: 'Quien se apunte',
};

function statusBadge(status) {
  return `<span class="badge" title="${escapeHtml(distributionStatusHints[status] ?? '')}">${escapeHtml(distributionStatusLabels[status] ?? status)}</span>`;
}

function formatDateTime(iso) {
  const date = new Date(iso);
  const time = `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`;
  return `${formatDate(iso)} ${time}`;
}

// "Talla: M" — what was chosen for a unit. With nothing chosen it is "Sin
// elegir" when the article has options (the own unit of everybody in a
// distribution for all members exists before anybody chooses), and a dash when
// there was nothing to choose.
function choicesText(choices, hasOptions = false) {
  const entries = Object.entries(choices ?? {});
  if (entries.length) return entries.map(([name, value]) => `${name}: ${value}`).join(', ');
  return hasOptions ? 'Sin elegir' : '—';
}

// A unit with options that nobody has chosen yet.
function isUnchosen(unit, hasOptions) {
  return hasOptions && Object.keys(unit.choices ?? {}).length === 0;
}

// The options textarea takes one option per line, "Talla: S, M, L".
function parseOptions(text) {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const colon = line.indexOf(':');
      const name = colon < 0 ? line : line.slice(0, colon);
      const values = colon < 0 ? [] : line.slice(colon + 1).split(',');
      return { name: name.trim(), values: values.map((v) => v.trim()).filter(Boolean) };
    });
}

function optionsText(options) {
  return (options ?? []).map((o) => `${o.name}: ${o.values.join(', ')}`).join('\n');
}

// datetime-local wants "YYYY-MM-DDTHH:MM" in the viewer's own time.
function toLocalInputValue(iso) {
  const date = new Date(iso);
  const pad = (n) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

// The body of POST/PATCH /distributions from the shared form (fields named
// as in the API). Empty optional fields go as null: a PATCH replaces them.
function readDistributionForm(form) {
  const data = new FormData(form);
  const text = (name) => data.get(name)?.toString().trim() ?? '';
  const number = (name) => (text(name) === '' ? null : Number(text(name).replace(',', '.')));
  return {
    name: text('name'),
    description: text('description') || null,
    audience: text('audience'),
    cutoff_date: text('cutoff_date') || null,
    closes_at: text('closes_at') ? new Date(text('closes_at')).toISOString() : null,
    price: number('price') ?? 0,
    extra_price: number('extra_price'),
    max_extra: number('max_extra'),
    options: parseOptions(text('options')),
  };
}

function fillDistributionForm(form, d) {
  const set = (name, value) => { form.elements[name].value = value ?? ''; };
  set('name', d.name);
  set('description', d.description);
  set('audience', d.audience);
  set('cutoff_date', d.cutoff_date ? d.cutoff_date.slice(0, 10) : '');
  set('closes_at', d.closes_at ? toLocalInputValue(d.closes_at) : '');
  set('price', d.price);
  set('extra_price', d.extra_price);
  set('max_extra', d.max_extra);
  set('options', optionsText(d.options));
}

// The people of a distribution's pages. `names` (login to full name, for
// everybody on file, to show who did what) comes from the directory. `entries`
// are what the "a nombre de" and "entregado a" pickers offer — a searchable
// text input — and they come from the distribution itself, so that only members
// can be chosen: purpose "order" gives those who may have it (members on its
// cutoff date, or today's when it has none) and "pickup" also whoever is a
// member now. Without a purpose there are no entries, for a page with no picker.
async function loadPeople(distributionId, purpose = null) {
  let names = {};
  try {
    const directory = await apiFetch('/users/directory');
    names = Object.fromEntries(directory.map((p) => [p.login, `${p.first_name} ${p.last_name}`]));
  } catch {
    // Without names the logins show instead.
  }
  let entries = [];
  if (purpose) {
    try {
      const members = await apiFetch(`/distributions/${distributionId}/people?for=${purpose}`);
      entries = members
        .map((p) => ({ id: p.login, label: pickerLabel(`${p.first_name} ${p.last_name}`, p.login) }))
        .sort((a, b) => a.label.localeCompare(b.label, 'es'));
    } catch {
      // No picker: the page says so when somebody tries to choose.
    }
  }
  return { entries, names };
}

// What the API's error codes say, in the club's own words. Anything not here
// falls back to the API's message.
const errorMessages = {
  not_open: 'Este reparto no admite pedidos ahora mismo (no está abierto, o ya pasó la fecha límite).',
  not_eligible: 'Solo pueden pedirlo socios: quienes lo eran en la fecha de corte (o, si no hay, los socios de hoy), tanto la persona para quien es como quien lo pide.',
  invalid_collector: 'Quien recoge tiene que ser socio ahora o haberlo sido en la fecha de corte.',
  already_registered: 'Esa persona ya tiene su pedido. Si quieres más, pide un extra.',
  extras_not_allowed: 'Este reparto no tiene extras.',
  too_many_extras: 'Esa persona ya tiene el máximo de extras.',
  invalid_choices: 'Elige un valor en cada opción.',
  automatic_unit: 'En un reparto para todos los socios cada uno tiene su pedido y no se puede quitar. Si no lo quiere, que no lo recoja.',
  unit_locked: 'Este pedido ya está pagado o recogido y no se puede cambiar.',
  not_delivering: 'Solo se puede apuntar una recogida con el reparto «En entrega».',
  already_picked_up: 'Alguno de los pedidos ya estaba recogido.',
  already_paid: 'Alguno de los pedidos ya estaba pagado.',
  has_units: 'Ya hay pedidos (desde que se abre el reparto): no se pueden cambiar las opciones.',
  has_settled_units: 'Hay pedidos ya pagados o recogidos: quita primero esas marcas para poder borrar el reparto.',
  invalid_status_transition: 'El estado solo puede avanzar o retroceder un paso cada vez.',
  cutoff_required: 'Un reparto para todos los socios necesita la fecha de corte.',
  invalid_max_extra: 'El máximo de extras necesita un precio para los extras, y no puede ser negativo.',
  invalid_options: 'Cada cosa a elegir necesita un nombre y al menos un valor, sin repetirlos.',
  invalid_price: 'Los precios no pueden ser negativos.',
};

function apiErrorText(err) {
  return errorMessages[err.code] ?? err.message;
}

// The body PATCH wants to leave a distribution as it is (it replaces every
// field), with whatever changes on top.
function bodyFrom(d, changes = {}) {
  return {
    name: d.name,
    description: d.description ?? null,
    audience: d.audience,
    cutoff_date: d.cutoff_date ? d.cutoff_date.slice(0, 10) : null,
    closes_at: d.closes_at ?? null,
    price: d.price,
    extra_price: d.extra_price ?? null,
    max_extra: d.max_extra ?? null,
    ...changes,
  };
}

export {
  errorMessages,
  apiErrorText,
  bodyFrom,
  distributionStatusLabels,
  distributionStatusHints,
  STATUS_ORDER,
  audienceLabels,
  statusBadge,
  formatDateTime,
  choicesText,
  isUnchosen,
  readDistributionForm,
  fillDistributionForm,
  loadPeople,
  resolvePickerId,
};

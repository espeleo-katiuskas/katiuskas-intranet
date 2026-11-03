// What /repartos, /reparto and /reparto-gestion share: the labels of a distribution's states, how its
// form is read and filled, how choices and people are shown, and the
// searchable people picker. The server (internal/distributions) is the
// authority on every rule; nothing here decides who may do what beyond hiding
// controls.
import {
  apiFetch,
  escapeHtml,
  formatCurrency,
  formatDate,
  formatDateTime,
  pickerLabel,
  resolvePickerId,
} from './api.js';

const distributionStatusLabels = {
  draft: 'Borrador',
  open: 'Se puede pedir',
  closed: 'Activo',
  done: 'Terminado',
};

// What each state means, for a tooltip on its badge.
const distributionStatusHints = {
  draft: 'Se puede editar. Los socios todavía no lo ven.',
  open: 'Los socios pueden hacer su pedido y elegir las opciones. También se puede ir entregando.',
  closed: 'Ya no se puede pedir ni cambiar nada. Se puede seguir entregando.',
  done: 'El reparto ha terminado.',
};

const STATUS_ORDER = ['draft', 'open', 'closed', 'done'];

// A distribution with articles hands out several things in any quantity (the
// lottery's papeletas and décimos) instead of one article with a price of its
// own: people ask for a number of each, and each can have a limit in all.
function hasArticles(d) {
  return (d.articles ?? []).length > 0;
}

// How many units of the article are left, or null when there is no limit.
function articleLeft(article) {
  return article.max_total == null ? null : Math.max(0, article.max_total - article.requested);
}

// What a row costs: a unit's price times how many it stands for.
function unitCost(u) {
  return u.price * (u.quantity ?? 1);
}

// How many units of a row are still to be paid (a row can be paid in part), and
// what they cost.
function pendingQuantity(u) {
  return (u.quantity ?? 1) - (u.paid_quantity ?? 0);
}

function pendingCost(u) {
  return u.price * pendingQuantity(u);
}

// A distribution for all members with no options to choose and no extras has
// nothing for members to do.
function nothingToAsk(d) {
  return d.audience === 'all_members' && d.options.length === 0 && d.extra_price == null;
}

const audienceLabels = {
  all_members: 'Todos los socios',
  signup: 'Quien se apunte',
};

// Who can see a distribution at all (its `visibility`); each includes the ones below.
const visibilityLabels = {
  all: 'Todo el mundo',
  material: 'Material, Junta directiva y administración',
  board: 'Junta directiva y administración',
  admin: 'Solo administración',
};

// The badge of a state, with its tooltip; `label` writes something else than the state's own name.
function statusBadge(status, label = distributionStatusLabels[status] ?? status) {
  return `<span class="badge" title="${escapeHtml(distributionStatusHints[status] ?? '')}">${escapeHtml(label)}</span>`;
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

// What a unit is, in a few words: "Papeleta × 7" for a row of an article,
// otherwise what was chosen (see choicesText).
function unitLabel(u, hasOptions = false, articles = []) {
  if (u.article_name) {
    const article = articles.find((a) => a.id === u.article_id);
    const numbers = u.identifiers?.length ? ` (${formatIdentifiers(article, u.identifiers)})` : '';
    return `${u.article_name} × ${u.quantity}${numbers}`;
  }
  return choicesText(u.choices, hasOptions);
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

// The articles textarea takes one article per line: "Papeletas: 5, máximo 200",
// the price first (it may use a comma for decimals) and then, in any order and
// all optional, "máximo N" (how many there are in all), "N por persona" and "con
// números" (each unit has its own number, written down when it is handed out).
// The numbers can be described, one indented line per part of the number with
// the values it can take, and then every combination is an item that exists
// (and the maximum is how many there are):
//   Décimos: 20
//     Serie: 124-133, 137
//     Fracción: 1-10
const ARTICLE_PRICE = /^(.+?)\s*:\s*(\d+(?:[.,]\d+)?)\s*€?\s*(?:,(.*))?$/;
const NUMBERING_LINE = /^(.+?)\s*:\s*(\S.*)$/;

function parseArticles(text) {
  const articles = [];
  for (const raw of text.split('\n')) {
    const line = raw.trim();
    if (!line) continue;
    const bad = (what) =>
      new Error(
        `No entiendo la línea «${line}» de los artículos. ${what} Por ejemplo «Papeletas: 5, máximo 200, 10 por persona», y debajo, con sangría, «Número: 0001-0200».`,
      );
    // An indented line (or one starting with a dash) describes the numbers of the
    // article above it.
    if (/^(\s+|-\s)/.test(raw) && articles.length) {
      const match = NUMBERING_LINE.exec(line.replace(/^-\s+/, ''));
      if (!match) throw bad('Usa «Nombre: valores», por ejemplo «Serie: 124-133, 137».');
      articles.at(-1).identifier_fields.push({ name: match[1].trim(), spec: match[2].trim() });
      continue;
    }
    const match = ARTICLE_PRICE.exec(line);
    if (!match)
      throw bad(
        'Usa «Nombre: precio», seguido si quieres de «máximo N», «N por persona» y «con números».',
      );
    const article = {
      name: match[1].trim(),
      price: Number(match[2].replace(',', '.')),
      max_total: null,
      max_per_person: null,
      has_identifiers: false,
      identifier_fields: [],
    };
    for (const part of (match[3] ?? '').split(',').map((p) => p.trim())) {
      const max = /^m[aá]x(?:imo|\.)?\s*(\d+)$/i.exec(part);
      const perPerson = /^(\d+)\s*por persona$/i.exec(part);
      if (!part) continue;
      if (max) article.max_total = Number(max[1]);
      else if (perPerson) article.max_per_person = Number(perPerson[1]);
      else if (/^con n[uú]meros$/i.test(part)) article.has_identifiers = true;
      else throw bad('Usa «máximo N», «N por persona» o «con números» después del precio.');
    }
    articles.push(article);
  }
  return articles;
}

function articlesText(articles) {
  return (articles ?? [])
    .map((a) => {
      const numbered = (a.identifier_fields ?? []).length > 0;
      const head = [
        `${a.name}: ${a.price}`,
        // With the numbers described the maximum is how many there are.
        a.max_total != null && !numbered ? `máximo ${a.max_total}` : null,
        a.max_per_person != null ? `${a.max_per_person} por persona` : null,
        a.has_identifiers && !numbered ? 'con números' : null,
      ]
        .filter(Boolean)
        .join(', ');
      const fields = (a.identifier_fields ?? []).map((f) => `  ${f.name}: ${f.spec}`);
      return [head, ...fields].join('\n');
    })
    .join('\n');
}

// Numbers in order: part by part (a number is its parts joined by a slash), the parts
// that are all digits as numbers ("9" before "10"; the same number with leading zeros
// shortest first) and any other text as text.
function compareIdentifiers(a, b) {
  const pa = a.split('/');
  const pb = b.split('/');
  for (let i = 0; i < Math.min(pa.length, pb.length); i++) {
    const x = pa[i];
    const y = pb[i];
    if (/^\d+$/.test(x) && /^\d+$/.test(y)) {
      const diff = Number(x) - Number(y);
      if (diff !== 0) return diff;
      if (x.length !== y.length) return x.length - y.length;
    } else {
      const diff = x.localeCompare(y, 'es', { numeric: true });
      if (diff !== 0) return diff;
    }
  }
  return pa.length - pb.length;
}

function sortIdentifiers(identifiers) {
  return [...(identifiers ?? [])].sort(compareIdentifiers);
}

// A list of numbers with nothing known of the article they belong to (a message, an
// entry of the list of payments), sorted and in ranges, in the form they are typed
// ("124/1-10").
function compactNumbers(identifiers) {
  const pseudo = {
    identifier_fields: identifiers.some((i) => i.includes('/')) ? [{ name: '' }, { name: '' }] : [],
  };
  return formatIdentifiers(pseudo, identifiers, false);
}

// The numbers somebody has, as short as they can be said: consecutive ones become
// a range ("0001-0050"), and in an article with several parts each is named
// ("Serie 124 · Fracción 1-10"), unless named is false, which gives them in the
// form the API takes ("124/1-10").
function formatIdentifiers(article, identifiers, named = true) {
  const fields = article?.identifier_fields ?? [];
  const parts = (id) => id.split('/');
  const runs = [];
  // Sorted first, so that consecutive numbers are next to each other whatever order
  // they come in, and join into the longest ranges there are.
  for (const id of sortIdentifiers(identifiers)) {
    const p = parts(id);
    const last = p.at(-1);
    const run = runs.at(-1);
    const sameKind = run && run.prefix === p.slice(0, -1).join('/');
    const consecutive =
      sameKind &&
      /^\d+$/.test(last) &&
      /^\d+$/.test(run.to) &&
      (last.length === run.to.length || (!last.startsWith('0') && !run.to.startsWith('0'))) &&
      Number(last) === Number(run.to) + 1;
    if (consecutive) run.to = last;
    else runs.push({ prefix: p.slice(0, -1).join('/'), from: last, to: last });
  }
  return runs
    .map((r) => {
      const range = r.from === r.to ? r.from : `${r.from}-${r.to}`;
      if (fields.length < 2) return range;
      // As it can be typed back, with the parts joined by a slash.
      if (!named) return r.prefix ? `${r.prefix}/${range}` : range;
      const labelled = r.prefix
        .split('/')
        .map((v, i) => `${fields[i]?.name ?? ''} ${v}`.trim())
        .concat(`${fields.at(-1).name} ${range}`);
      return labelled.join(' · ');
    })
    .join(', ');
}

// With articles, what only fits a distribution without them is disabled in a
// form and ignored when it is read: it is for whoever signs up, with no price,
// extras or options of its own.
const ARTICLE_EXCLUDED_FIELDS = ['audience', 'price', 'extra_price', 'max_extra', 'options'];

function syncArticleFields(form) {
  const on = form.elements.articles.value.trim() !== '';
  for (const name of ARTICLE_EXCLUDED_FIELDS) form.elements[name].disabled = on;
  if (on) form.elements.audience.value = 'signup';
  form.querySelector('[data-articles-hint]').hidden = !on;
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
  const articles = parseArticles(text('articles'));
  const common = {
    name: text('name'),
    description: text('description') || null,
    cutoff_date: text('cutoff_date') || null,
    closes_at: text('closes_at') ? new Date(text('closes_at')).toISOString() : null,
    account_id: text('account_id') ? Number(text('account_id')) : null,
    articles,
    // Only an administrator has the field (the page removes it for anybody else); left out, the
    // API keeps what a distribution has, or "all" for a new one.
    ...(form.elements.visibility ? { visibility: text('visibility') } : {}),
  };
  // The fields of a distribution with articles are disabled, so they aren't in
  // the form data; what the API wants for it is spelled out here.
  if (articles.length) {
    return {
      ...common,
      audience: 'signup',
      price: 0,
      extra_price: null,
      max_extra: null,
      options: [],
    };
  }
  return {
    ...common,
    audience: text('audience'),
    price: number('price') ?? 0,
    extra_price: number('extra_price'),
    max_extra: number('max_extra'),
    options: parseOptions(text('options')),
  };
}

// Offers, in the form's account select, "the default" (empty: the account marked
// for the payments of distributions, named when there is one) and every account
// of type income that isn't hidden.
async function loadAccountOptions(form) {
  const select = form.elements.account_id;
  try {
    const all = await apiFetch('/accounts');
    const groups = new Set(all.map((a) => a.parent_id));
    const accounts = all.filter(
      (a) => !a.hidden && a.effective_type === 'income' && !groups.has(a.id),
    );
    const standard = accounts.find((a) => a.distribution_default);
    select.innerHTML = [
      `<option value="">${standard ? `Por defecto (${escapeHtml(standard.name)})` : 'Por defecto (todavía no hay ninguna)'}</option>`,
      ...accounts.map((a) => `<option value="${a.id}">${escapeHtml(a.name)}</option>`),
    ].join('');
  } catch {
    select.innerHTML = '<option value="">Por defecto</option>';
  }
}

function fillDistributionForm(form, d) {
  const set = (name, value) => {
    form.elements[name].value = value ?? '';
  };
  set('name', d.name);
  set('description', d.description);
  set('audience', d.audience);
  if (form.elements.visibility) set('visibility', d.visibility);
  set('cutoff_date', d.cutoff_date ? d.cutoff_date.slice(0, 10) : '');
  set('closes_at', d.closes_at ? toLocalInputValue(d.closes_at) : '');
  set('account_id', d.account_id);
  set('price', d.price);
  set('extra_price', d.extra_price);
  set('max_extra', d.max_extra);
  set('options', optionsText(d.options));
  set('articles', articlesText(d.articles));
  syncArticleFields(form);
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
        .map((p) => ({
          id: p.login,
          label: pickerLabel(`${p.first_name} ${p.last_name}`, p.login),
        }))
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
  not_open:
    'Este reparto no admite pedidos ahora mismo (no está abierto, o ya pasó la fecha límite).',
  unknown_account: 'Esa cuenta no existe.',
  account_not_asset:
    'La cuenta en la que se paga tiene que ser de tipo «activo» (caja, banco…). El tipo se cambia en Contabilidad.',
  account_required: 'Elige la cuenta en la que se ha pagado.',
  account_not_income:
    'La cuenta de los pagos de un reparto tiene que ser de tipo «ingresos». El tipo se cambia en Contabilidad.',
  account_has_children:
    'Una cuenta que agrupa otras cuentas no puede tener transacciones: usa una de sus cuentas hijas.',
  no_distribution_account:
    'Este reparto no tiene cuenta para sus pagos y no hay una cuenta por defecto: elige una al editarlo, o marca una en Contabilidad como cuenta por defecto de los repartos.',
  unit_paid: 'Este pedido tiene pagos: quítalos antes de borrarlo.',
  not_eligible:
    'Solo pueden pedirlo socios: quienes lo eran en la fecha de corte (o, si no hay, los socios de hoy), tanto la persona para quien es como quien lo pide.',
  invalid_collector: 'Quien recoge tiene que ser socio ahora o haberlo sido en la fecha de corte.',
  already_registered: 'Esa persona ya tiene su pedido.',
  extras_not_allowed: 'Este reparto no tiene extras.',
  too_many_extras: 'Esa persona ya tiene el máximo de extras.',
  invalid_choices: 'Elige un valor en cada opción.',
  automatic_unit:
    'En un reparto para todos los socios cada uno tiene su pedido y no se puede quitar. Si no lo quiere, que no lo recoja.',
  unit_locked: 'Este pedido ya está pagado o recogido y no se puede cambiar.',
  not_deliverable: 'No se puede entregar con el reparto en borrador.',
  already_picked_up: 'Se intenta entregar más unidades de las que quedan por entregar.',
  already_paid:
    'Alguno de los pedidos ya estaba pagado, o se marcan más unidades de las que quedan por pagar.',
  has_units:
    'Ya hay pedidos (desde que se abre el reparto): no se pueden cambiar las opciones ni los artículos, salvo el límite de cada uno.',
  has_settled_units: 'Hay pedidos ya pagados o recogidos: quita primero esas marcas.',
  invalid_status_transition: 'El estado solo puede avanzar o retroceder un paso cada vez.',
  cutoff_required: 'Un reparto para todos los socios necesita la fecha de corte.',
  invalid_max_extra:
    'El máximo de extras necesita un precio para los extras, y no puede ser negativo.',
  invalid_options: 'Cada cosa a elegir necesita un nombre y al menos un valor, sin repetirlos.',
  invalid_price: 'Los precios no pueden ser negativos.',
  invalid_articles:
    'Cada artículo necesita un nombre distinto, un precio de 0 o más y, si tiene límite, uno de al menos 1.',
  articles_conflict:
    'Un reparto con artículos es para quien se apunte y no tiene precio, extras ni opciones propios (cada artículo tiene su precio).',
  article_required: 'Elige de qué artículo quieres pedir.',
  invalid_quantity: 'La cantidad tiene que ser un número entre 1 y 100000.',
  unknown_article: 'Ese artículo no es de este reparto.',
  article_exhausted: 'No quedan tantas unidades de ese artículo.',
  article_person_limit: 'Es más de lo que puede tener una persona de ese artículo.',
  invalid_identifiers:
    'Los números no pueden estar vacíos ni repetidos, ni ser más que la cantidad, y solo valen para artículos con números.',
  identifier_not_assigned: 'Alguno de esos números no está asignado a este pedido.',
  identifier_not_paid:
    'Alguno de esos números no está pagado, así que no se le puede quitar el pago.',
  identifier_not_delivered: 'Alguno de esos números no está entregado.',
  identifier_taken: 'Otra persona ya tiene alguno de esos números de ese artículo.',
  quantity_below_numbered:
    'No se puede pedir menos de los números ya asignados: quita antes el número a los que sobren.',
  quantity_below_delivered:
    'No se puede pedir menos de lo que ya se ha entregado: devuelve antes lo que sobra.',
  quantity_below_paid:
    'El pedido no puede quedar por debajo de lo que ya está pagado: quita antes la marca de pagado.',
  invalid_numbering:
    'Cada parte del número necesita un nombre distinto y valores como «124-133, 137» (sin barras), con 100000 combinaciones como mucho.',
  number_in_use: 'Ese cambio dejaría fuera un número que ya tiene alguien.',
  not_enough_numbers: 'No quedan números libres suficientes.',
  article_not_numbered: 'Los números de ese artículo no están descritos.',
  identifiers_missing:
    'Faltan números: antes de entregar un artículo con números hay que apuntar todos los de cada pedido.',
  cap_below_requested: 'El límite de un artículo no puede ser menor que lo que ya se ha pedido.',
};

// With the distribution at hand, a message can say what is possible in it: that
// somebody already has their order only suggests asking for an extra when this
// distribution has extras.
// The errors about numbers end with which numbers they are about ("…: 0003, 0004");
// those are said after the message, and the generic "already paid / handed out" ones
// say it is about those numbers when there are any.
const ABOUT_NUMBERS = new Set([
  'identifier_not_assigned',
  'identifier_not_paid',
  'identifier_not_delivered',
  'quantity_below_paid',
]);

function numbersMentioned(err) {
  return /: (.+)$/.exec(err.message ?? '')?.[1] ?? null;
}

function apiErrorText(err, distribution = null) {
  const numbers = numbersMentioned(err);
  const shown = numbers ? compactNumbers(numbers.split(', ')) : null;
  if (shown && ABOUT_NUMBERS.has(err.code)) {
    return `${errorMessages[err.code]} (${shown})`;
  }
  if (shown && err.code === 'already_paid') return `Ya estaban pagados: ${shown}.`;
  if (shown && err.code === 'already_picked_up') return `Ya estaban entregados: ${shown}.`;
  if (err.code === 'already_registered' && hasArticles(distribution ?? {})) {
    return 'Esa persona ya ha pedido ese artículo: cambia la cantidad en «Tus pedidos».';
  }
  if (err.code === 'already_registered' && distribution?.extra_price != null) {
    return `${errorMessages.already_registered} Si quieres más, pide un extra.`;
  }
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
    account_id: d.account_id ?? null,
    price: d.price,
    extra_price: d.extra_price ?? null,
    max_extra: d.max_extra ?? null,
    ...changes,
  };
}

// ----- what a distribution is, in a line -----

// "Papeleta 5,00 € (quedan 120 de 200)": an article with what it costs and, when
// there is a limit in all, how many are left.
function articleText(a) {
  const price = a.price > 0 ? formatCurrency(a.price) : 'gratis';
  const left = articleLeft(a);
  const notes = [
    left == null ? null : `quedan ${left} de ${a.max_total}`,
    a.max_per_person == null ? null : `hasta ${a.max_per_person} por persona`,
  ].filter(Boolean);
  return `${escapeHtml(a.name)} ${price}${notes.length ? ` (${notes.join(', ')})` : ''}`;
}

// What this distribution is, in a line.
function describeDistribution(d) {
  const parts = [statusBadge(d.status), escapeHtml(audienceLabels[d.audience])];
  if (d.visibility && d.visibility !== 'all') {
    parts.push(`lo ven: ${escapeHtml(visibilityLabels[d.visibility])}`);
  }
  if (d.cutoff_date) {
    parts.push(
      d.audience === 'signup'
        ? `solo socios a ${formatDate(d.cutoff_date)}`
        : `socios a ${formatDate(d.cutoff_date)}`,
    );
  }
  if (hasArticles(d)) {
    parts.push(d.articles.map(articleText).join(' · '));
  } else {
    parts.push(d.price > 0 ? `${formatCurrency(d.price)} cada uno` : 'gratis');
    if (d.extra_price != null) {
      const cap = d.max_extra != null ? `, hasta ${d.max_extra} por socio` : '';
      parts.push(`extras a ${formatCurrency(d.extra_price)}${cap}`);
    }
  }
  if (d.closes_at) parts.push(`se elige hasta el ${formatDateTime(d.closes_at)}`);
  return parts.join(' · ');
}

export {
  errorMessages,
  describeDistribution,
  apiErrorText,
  bodyFrom,
  loadAccountOptions,
  distributionStatusLabels,
  distributionStatusHints,
  STATUS_ORDER,
  nothingToAsk,
  hasArticles,
  articleLeft,
  unitCost,
  pendingQuantity,
  pendingCost,
  unitLabel,
  parseArticles,
  formatIdentifiers,
  sortIdentifiers,
  compactNumbers,
  syncArticleFields,
  audienceLabels,
  visibilityLabels,
  statusBadge,
  formatDateTime,
  choicesText,
  isUnchosen,
  readDistributionForm,
  fillDistributionForm,
  loadPeople,
  resolvePickerId,
};

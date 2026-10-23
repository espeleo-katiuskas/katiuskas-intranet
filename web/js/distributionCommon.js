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

// A distribution for all members with no options to choose and no extras has
// nothing for members to do.
function nothingToAsk(d) {
  return d.audience === 'all_members' && d.options.length === 0 && d.extra_price == null;
}

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

// What a unit is, in a few words: "Papeleta × 7" for a row of an article,
// otherwise what was chosen (see choicesText).
function unitLabel(u, hasOptions = false) {
  if (u.article_name) {
    const numbers = u.identifiers?.length ? ` (${u.identifiers.join(', ')})` : '';
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
const ARTICLE_PRICE = /^(.+?)\s*:\s*(\d+(?:[.,]\d+)?)\s*€?\s*(?:,(.*))?$/;

function parseArticles(text) {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const bad = () =>
        new Error(
          `No entiendo la línea «${line}» de los artículos. Usa «Nombre: precio», seguido si quieres de «máximo N», «N por persona» y «con números», por ejemplo «Papeletas: 5, máximo 200, 10 por persona, con números».`,
        );
      const match = ARTICLE_PRICE.exec(line);
      if (!match) throw bad();
      const article = {
        name: match[1].trim(),
        price: Number(match[2].replace(',', '.')),
        max_total: null,
        max_per_person: null,
        has_identifiers: false,
      };
      for (const part of (match[3] ?? '').split(',').map((p) => p.trim())) {
        const max = /^m[aá]x(?:imo|\.)?\s*(\d+)$/i.exec(part);
        const perPerson = /^(\d+)\s*por persona$/i.exec(part);
        if (!part) continue;
        if (max) article.max_total = Number(max[1]);
        else if (perPerson) article.max_per_person = Number(perPerson[1]);
        else if (/^con n[uú]meros$/i.test(part)) article.has_identifiers = true;
        else throw bad();
      }
      return article;
    });
}

function articlesText(articles) {
  return (articles ?? [])
    .map((a) =>
      [
        `${a.name}: ${a.price}`,
        a.max_total != null ? `máximo ${a.max_total}` : null,
        a.max_per_person != null ? `${a.max_per_person} por persona` : null,
        a.has_identifiers ? 'con números' : null,
      ]
        .filter(Boolean)
        .join(', '),
    )
    .join('\n');
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
    articles,
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

function fillDistributionForm(form, d) {
  const set = (name, value) => {
    form.elements[name].value = value ?? '';
  };
  set('name', d.name);
  set('description', d.description);
  set('audience', d.audience);
  set('cutoff_date', d.cutoff_date ? d.cutoff_date.slice(0, 10) : '');
  set('closes_at', d.closes_at ? toLocalInputValue(d.closes_at) : '');
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
  already_picked_up: 'Alguno de los pedidos ya estaba recogido.',
  already_paid: 'Alguno de los pedidos ya estaba pagado.',
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
  identifier_taken: 'Otra persona ya tiene alguno de esos números de ese artículo.',
  identifiers_missing:
    'Faltan números: antes de entregar un artículo con números hay que apuntar todos los de cada pedido.',
  cap_below_requested: 'El límite de un artículo no puede ser menor que lo que ya se ha pedido.',
};

// With the distribution at hand, a message can say what is possible in it: that
// somebody already has their order only suggests asking for an extra when this
// distribution has extras.
function apiErrorText(err, distribution = null) {
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
  nothingToAsk,
  hasArticles,
  articleLeft,
  unitCost,
  unitLabel,
  parseArticles,
  syncArticleFields,
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

// The "Historial como socio" timeline shared by /member (which adds its action
// buttons to each row) and /profile (which shows it as is): one flat,
// chronological list of everything that happened to a person as a socio, plus
// the records it was built from, which /member's own actions need to look up.

import { apiFetch, escapeHtml, formatDate, positionLabels } from './api.js';

// Which events are "cambios de estado" (alta, baja, inicio/fin de baja
// temporal), not licenses or board terms, which have their own independent
// delete affordance already. Only the most recent one overall ever gets a
// "Borrar" button on /member: undoing history further back than the single
// most recent action isn't offered, only correcting it (Editar).
const STATUS_EVENT_TYPES = new Set(['alta', 'baja', 'leave_start', 'leave_end']);

// board_start/board_end aren't here — their own text always folds the
// position in a fixed shape ("Nuevo cargo: X"/"Deja el cargo de X"),
// never the generic "label · reason" every other event type uses (see
// timelineEventText below), so a plain label to concatenate a reason
// onto wouldn't fit them anyway.
const eventLabels = {
  alta: 'Alta',
  baja: 'Baja',
  leave_start: 'Inicio de baja temporal',
  leave_end: 'Fin de baja temporal',
  license: 'Licencia federativa',
  due: 'Cuota anual',
};

// The text a timeline row shows next to its date — every type but the
// board ones is just "label" or "label · reason"; a board term's own
// reason (see buildTimelineEvents) is always the position name, and
// reads better folded directly into the sentence.
function timelineEventText(event) {
  if (event.type === 'board_start') return `Nuevo cargo: ${escapeHtml(event.reason)}`;
  if (event.type === 'board_end') return `Deja el cargo de ${escapeHtml(event.reason)}`;
  const reasonText = event.reason ? ` · ${escapeHtml(event.reason)}` : '';
  return `${eventLabels[event.type]}${reasonText}`;
}


// Same-date tie-break for the timeline sort below: an alta or a fin de
// baja temporal (both "the person is active as of today") always shows
// before a baja or an inicio de baja temporal dated the same day (both
// "the person stops being active as of today") — otherwise it could
// read as if they'd left before rejoining, or started a leave before
// ending the previous one, on the very same day. Every other event
// type (license, board terms) sits at the neutral middle priority and
// keeps its original relative order among ties, same as before. A cuota
// anual goes between the two: after the alta that starts the membership
// it's paid for, but before a license paid the same day — the cuota is
// what makes someone a socio able to be federated, and it's what an
// admin records first.
const TIMELINE_TIE_BREAK_PRIORITY = { alta: 0, leave_end: 0, due: 0.5, baja: 2, leave_start: 2 };

// Builds one flat, chronological list of events out of every membership
// period, every temporary leave this person has ever taken (belongs to
// them directly, not to one specific period — a single GET
// /users/{login}/leaves covers every alta/baja span they've had, not
// just whichever one happens to be current), every federative license
// this person has ever been issued, every annual cuota they've paid
// (dated by the payment itself — paid_at, its transaction's date — not
// by when it was recorded; there's no edit or delete for one, so unlike
// a license it never gets buttons), and every board term they've
// served (those are scoped to the person directly, not to any one
// membership period, the same way licenses are).
async function loadTimelineData(login, memberships) {
  const events = [];
  const membershipsById = Object.fromEntries(memberships.map((m) => [m.id, m]));
  const openMembership = memberships.find((m) => !m.end_date) ?? null;
  for (const m of memberships) {
    events.push({ date: m.start_date, type: 'alta', membership: m, statusKey: `alta-${m.id}` });
    if (m.end_date) {
      events.push({ date: m.end_date, type: 'baja', membership: m, reason: m.end_reason, statusKey: `baja-${m.id}` });
    }
  }

  const leaves = await apiFetch(`/users/${login}/leaves`).catch(() => []);
  const leavesById = Object.fromEntries(leaves.map((l) => [l.id, l]));
  const openLeave = leaves.find((l) => !l.end_date) ?? null;
  for (const l of leaves) {
    events.push({ date: l.start_date, type: 'leave_start', leave: l, reason: l.reason, statusKey: `leave_start-${l.id}` });
    if (l.end_date) {
      events.push({ date: l.end_date, type: 'leave_end', leave: l, reason: l.reason, statusKey: `leave_end-${l.id}` });
    }
  }
  const membershipStatus = { openMembership, openLeave, lastMembership: memberships[0] ?? null };

  const licenses = await apiFetch(`/users/${login}/licenses`).catch(() => []);
  const licensesById = Object.fromEntries(licenses.map((l) => [l.id, l]));
  for (const l of licenses) {
    events.push({ date: l.issued_date, type: 'license', license: l, reason: l.type });
  }

  const dues = await apiFetch(`/users/${login}/dues`).catch(() => []);
  for (const d of dues) {
    // Same midnight-UTC shape every other event's date has (an alta's
    // start_date, a license's issued_date): the sort below compares these
    // as plain strings, so a bare "YYYY-MM-DD" would sort before an alta
    // dated the very same day instead of tying with it, and the
    // same-day tie-break (alta first) would never get to apply.
    const paidOn = `${(d.paid_at ?? d.created_at).slice(0, 10)}T00:00:00Z`;
    events.push({ date: paidOn, type: 'due', due: d, reason: String(d.year) });
  }

  const boardTerms = await apiFetch(`/users/${login}/board-members`).catch(() => []);
  const boardTermsById = Object.fromEntries(boardTerms.map((b) => [b.id, b]));
  const currentBoardTerm = boardTerms.find((b) => !b.end_date) ?? null;
  for (const b of boardTerms) {
    const positionLabel = positionLabels[b.position] ?? b.position;
    events.push({ date: b.start_date, type: 'board_start', boardMember: b, reason: positionLabel });
    if (b.end_date) {
      events.push({ date: b.end_date, type: 'board_end', boardMember: b, reason: positionLabel });
    }
  }

  events.sort((a, b) => {
    const dateCompare = a.date.localeCompare(b.date);
    if (dateCompare !== 0) return dateCompare;
    const priorityA = TIMELINE_TIE_BREAK_PRIORITY[a.type] ?? 1;
    const priorityB = TIMELINE_TIE_BREAK_PRIORITY[b.type] ?? 1;
    return priorityA - priorityB;
  });
  const lastStatusEventKey = events.filter((e) => STATUS_EVENT_TYPES.has(e.type)).at(-1)?.statusKey ?? null;
  return { events, membershipsById, leavesById, licensesById, boardTermsById, currentBoardTerm, membershipStatus, lastStatusEventKey };
}


// One row of the timeline: the date and what happened, and — on /member only —
// the buttons for it (already-built HTML, empty on /profile).
function renderTimelineRow(event, actionsHtml = '') {
  return `
    <div class="detail-row">
      <span>${formatDate(event.date)} — ${timelineEventText(event)}</span>
      <span>${actionsHtml}</span>
    </div>
  `;
}

export { loadTimelineData, renderTimelineRow, STATUS_EVENT_TYPES };

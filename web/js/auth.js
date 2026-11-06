import { goAfterLogin, setToken } from './api.js';
import { initPickerSearch } from './pickerSearch.js';

const API_BASE = '/api/v1';

// On a "local." address (the club's shared tablet: local.intranet.katiuskas.es) the API gives sessions
// of a few hours instead of a month — the same rule it applies, the first component of the host —
// and the page says so, before anybody signs in. The page is the same on every other address.
if (window.location.hostname.toLowerCase().split('.')[0] === 'local') {
  document.getElementById('local-notice').hidden = false;
}

// The last addresses that signed in on this browser, so that the field offers them instead of having to
// be typed (slow on a phone): only the email, not the phone, and only after a sign-in that worked. The
// address typed when asking for the link waits in PENDING_KEY until this browser completes the sign-in
// (by the code, or by the link opened in another tab of it), so a request that is never followed through
// is not remembered.
const EMAILS_KEY = 'katiuskas:login-emails';
const PENDING_KEY = 'katiuskas:login-pending-email';
const MAX_EMAILS = 5;

function savedEmails() {
  try {
    const list = JSON.parse(localStorage.getItem(EMAILS_KEY) ?? '[]');
    return Array.isArray(list) ? list.filter((e) => typeof e === 'string') : [];
  } catch {
    return [];
  }
}

// Puts the address first (once, whatever its case) and keeps the last MAX_EMAILS.
function rememberEmail(email) {
  const clean = (email ?? '').trim();
  if (!clean) return;
  try {
    const rest = savedEmails().filter((e) => e.toLowerCase() !== clean.toLowerCase());
    localStorage.setItem(EMAILS_KEY, JSON.stringify([clean, ...rest].slice(0, MAX_EMAILS)));
  } catch {
    // Without storage nothing is remembered: it is only a convenience.
  }
}

// The sign-in worked: the address asked with is now one to remember.
function signInSucceeded() {
  try {
    rememberEmail(localStorage.getItem(PENDING_KEY));
    localStorage.removeItem(PENDING_KEY);
  } catch {
    // See rememberEmail.
  }
}

const emailInput = document.getElementById('email');
function fillEmailOptions() {
  document.getElementById('email-options').innerHTML = savedEmails()
    .map((e) => `<option value="${e.replace(/&/g, '&amp;').replace(/"/g, '&quot;')}"></option>`)
    .join('');
}
fillEmailOptions();
initPickerSearch();
// Choosing one of them leaves the phone, the part still to be typed.
emailInput.addEventListener('change', () => {
  if (savedEmails().includes(emailInput.value)) document.getElementById('phone').focus();
});

const consumingPanel = document.getElementById('consuming-panel');
const consumingMessage = document.getElementById('consuming-message');
const requestForm = document.getElementById('request-form');
const feedbackBox = document.getElementById('request-feedback');
const errorBox = document.getElementById('request-error');
const codeForm = document.getElementById('code-form');
const codeInput = document.getElementById('code');
const codeErrorBox = document.getElementById('code-error');
const telegramInfo = document.getElementById('telegram-info');
const telegramBotLink = document.getElementById('telegram-bot-link');

// Remembered across the request → code step so the code can be checked
// without asking for email/phone again — the code alone is too short to
// look someone up by (see POST /auth/magic-link/consume-code).
let pendingEmail = '';
let pendingPhone = '';

function wireUpForms() {
  requestForm.addEventListener('submit', handleRequestSubmit);
  codeForm.addEventListener('submit', handleCodeSubmit);
  document.getElementById('request-again').addEventListener('click', () => {
    codeForm.hidden = true;
    requestForm.hidden = false;
  });
}

const magicLinkToken = new URLSearchParams(window.location.search).get('token');

if (magicLinkToken) {
  requestForm.hidden = true;
  consumingPanel.hidden = false;
  consumeMagicLink(magicLinkToken);
} else {
  await redirectIfAlreadySignedIn();
  watchForSignInElsewhere();
}

// The link in the email is usually opened in another window or tab, which
// signs in there while this one keeps asking for the code (or the email and
// phone). Writing the token to localStorage is what that sign-in does, and
// the browser tells every other window of the same site about it (the
// "storage" event); coming back to this tab (visibilitychange) is checked as
// well, for a browser that doesn't deliver the event. Either way, a token
// that really is valid (GET /auth/me, same as at load time) sends this
// window home, already signed in.
function watchForSignInElsewhere() {
  let redirecting = false;
  async function check() {
    const stored = localStorage.getItem('token');
    if (!stored || redirecting) return;
    try {
      const response = await fetch(`${API_BASE}/auth/me`, {
        headers: { Authorization: `Bearer ${stored}` },
      });
      if (response.ok) {
        redirecting = true;
        signInSucceeded();
        goAfterLogin();
      }
    } catch {
      // Network hiccup: stay on the form; the next event checks again.
    }
  }
  window.addEventListener('storage', (event) => {
    if (event.key === 'token' && event.newValue) check();
  });
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) check();
  });
}

// Someone already signed in on this browser has no reason to see the
// sign-in form again — checked against a stored token's real validity
// (GET /auth/me), not just its presence, since a stale/expired one
// should fall through to the ordinary form instead of a redirect loop.
// Skipped entirely when a fresh magic-link token is in the URL (handled
// above instead) — consuming that one always takes priority.
async function redirectIfAlreadySignedIn() {
  const existing = localStorage.getItem('token');
  if (!existing) {
    wireUpForms();
    return;
  }
  requestForm.hidden = true;
  consumingPanel.hidden = false;
  consumingMessage.textContent = 'Comprobando la sesión…';
  try {
    const response = await fetch(`${API_BASE}/auth/me`, {
      headers: { Authorization: `Bearer ${existing}` },
    });
    if (response.ok) {
      goAfterLogin();
      return;
    }
  } catch {
    // Network hiccup — fall through to the ordinary sign-in form below
    // rather than getting stuck on "Comprobando la sesión…" forever.
  }
  consumingPanel.hidden = true;
  requestForm.hidden = false;
  wireUpForms();
}

loadTelegramBotInfo();

// Shows the "sign in via Telegram" note only once we know there's a bot
// to actually message — username comes back empty when
// TELEGRAM_BOT_USERNAME isn't configured, and this call itself can fail
// (network hiccup); either way the page still works fine with just the
// email/phone flow, so this never blocks or shows an error of its own.
async function loadTelegramBotInfo() {
  try {
    const response = await fetch(`${API_BASE}/auth/telegram-bot`);
    if (!response.ok) return;
    const data = await response.json();
    if (!data.username) return;
    telegramBotLink.href = `https://t.me/${data.username}`;
    telegramInfo.hidden = false;
  } catch {
    // Nothing to show — see comment above.
  }
}

async function consumeMagicLink(rawToken) {
  try {
    const response = await fetch(`${API_BASE}/auth/magic-link/consume`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token: rawToken }),
    });
    if (!response.ok) {
      throw new Error('invalid token');
    }
    const data = await response.json();
    setToken(data.token);
    signInSucceeded();
    goAfterLogin();
  } catch {
    // consumingMessage lives inside #consuming-panel, which gets hidden
    // right below — setting the error there was a bug, never actually
    // visible to anyone: requestForm is what's on screen at this point,
    // so the error belongs on its own #request-error box instead.
    consumingPanel.hidden = true;
    requestForm.hidden = false;
    wireUpForms();
    errorBox.textContent = 'Este enlace no es válido o ha caducado. Por favor, pide uno nuevo.';
    errorBox.hidden = false;
  }
}

async function handleRequestSubmit(event) {
  event.preventDefault();
  errorBox.hidden = true;
  feedbackBox.hidden = true;

  const email = document.getElementById('email').value.trim();
  const phone = document.getElementById('phone').value.trim();
  const submitButton = requestForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;

  try {
    const response = await fetch(`${API_BASE}/auth/magic-link`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, phone }),
    });
    if (response.status === 404) {
      // unknown_details: nobody with access has that email and phone together. Said as it is,
      // since a typo is by far the likeliest cause and the person can fix it, or ask the board.
      errorBox.textContent =
        'Ese correo y ese teléfono no corresponden a nadie con acceso a la Intranet. Comprueba que los has escrito bien; si lo están, ponte en contacto con la Junta Directiva.';
      errorBox.hidden = false;
      return;
    }
    if (!response.ok) {
      throw new Error('request failed');
    }
    pendingEmail = email;
    pendingPhone = phone;
    try {
      localStorage.setItem(PENDING_KEY, email);
    } catch {
      // See rememberEmail.
    }
    requestForm.hidden = true;
    codeErrorBox.hidden = true;
    codeInput.value = '';
    codeForm.hidden = false;
    codeInput.focus();
  } catch {
    errorBox.textContent = 'No se ha podido enviar la solicitud. Inténtalo de nuevo en un momento.';
    errorBox.hidden = false;
  } finally {
    submitButton.disabled = false;
  }
}

async function handleCodeSubmit(event) {
  event.preventDefault();
  codeErrorBox.hidden = true;

  const code = codeInput.value.trim();
  const submitButton = codeForm.querySelector('button[type="submit"]');
  submitButton.disabled = true;

  try {
    const response = await fetch(`${API_BASE}/auth/magic-link/consume-code`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email: pendingEmail, phone: pendingPhone, code }),
    });
    if (!response.ok) {
      throw new Error('invalid code');
    }
    const data = await response.json();
    setToken(data.token);
    signInSucceeded();
    goAfterLogin();
  } catch {
    codeErrorBox.textContent = 'Código incorrecto o caducado.';
    codeErrorBox.hidden = false;
  } finally {
    submitButton.disabled = false;
  }
}

import { setToken } from './api.js';

const API_BASE = '/api/v1';

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
      window.location.href = '/';
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
    window.location.href = '/';
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
    if (!response.ok) {
      throw new Error('request failed');
    }
    pendingEmail = email;
    pendingPhone = phone;
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
    window.location.href = '/';
  } catch {
    codeErrorBox.textContent = 'Código incorrecto o caducado.';
    codeErrorBox.hidden = false;
  } finally {
    submitButton.disabled = false;
  }
}

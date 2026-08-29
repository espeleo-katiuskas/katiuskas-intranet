import { setAccessTokenFromLogin } from './api.js';

const API_BASE = '/api/v1';

const consumingPanel = document.getElementById('consuming-panel');
const consumingMessage = document.getElementById('consuming-message');
const requestForm = document.getElementById('request-form');
const feedbackBox = document.getElementById('request-feedback');
const errorBox = document.getElementById('request-error');

const token = new URLSearchParams(window.location.search).get('token');

if (token) {
  requestForm.hidden = true;
  consumingPanel.hidden = false;
  consumeMagicLink(token);
} else {
  requestForm.addEventListener('submit', handleRequestSubmit);
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
    setAccessTokenFromLogin(data.access_token);
    localStorage.setItem('refresh_token', data.refresh_token);
    window.location.href = '/';
  } catch {
    consumingMessage.textContent = 'Este enlace no es válido o ha caducado. Pide uno nuevo a continuación.';
    consumingPanel.hidden = true;
    requestForm.hidden = false;
    requestForm.addEventListener('submit', handleRequestSubmit);
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
    feedbackBox.textContent =
      'Si los datos coinciden con una cuenta con acceso, te hemos enviado un correo con un enlace para entrar.';
    feedbackBox.hidden = false;
    requestForm.reset();
  } catch {
    errorBox.textContent = 'No se ha podido enviar la solicitud. Inténtalo de nuevo en un momento.';
    errorBox.hidden = false;
  } finally {
    submitButton.disabled = false;
  }
}

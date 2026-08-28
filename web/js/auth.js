import { setAccessTokenFromLogin } from './api.js';

const API_BASE = '/api/v1';
const form = document.getElementById('login-form');
const errorBox = document.getElementById('login-error');

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  errorBox.hidden = true;

  const username = document.getElementById('username').value.trim();
  const password = document.getElementById('password').value;

  try {
    const response = await fetch(`${API_BASE}/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    });
    if (!response.ok) {
      throw new Error('invalid credentials');
    }
    const data = await response.json();
    setAccessTokenFromLogin(data.access_token);
    localStorage.setItem('refresh_token', data.refresh_token);
    window.location.href = 'index.html';
  } catch {
    errorBox.textContent = 'Usuario o contraseña incorrectos.';
    errorBox.hidden = false;
  }
});

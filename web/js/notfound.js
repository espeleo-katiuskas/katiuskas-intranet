// Served by Caddy for any address that isn't a page (see deploy/Caddyfile), so
// it can't assume anything about who is looking: with a session token it
// becomes a normal page of the app, menu included, and without one it shows a
// plain panel linking to the sign-in page. initPage() itself sends someone
// whose token has stopped working to /login, which is where they'd need to go.
let hasToken = false;
try {
  hasToken = !!localStorage.getItem('token');
} catch {
  // Storage blocked: nothing to sign in with.
}

if (hasToken) {
  document.getElementById('app-shell').hidden = false;
  // Loaded only now: api.js reads the token as soon as it is imported.
  const { initPage } = await import('./layout.js');
  await initPage();
} else {
  document.getElementById('public-shell').hidden = false;
}

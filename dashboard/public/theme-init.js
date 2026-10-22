// Applies the stored theme before the app bundle loads, avoiding a light/dark flash.
// Kept as a static file (not inline) so nginx can serve a CSP without 'unsafe-inline' scripts.
(function () {
  var stored = null;
  try {
    stored = window.localStorage.getItem('theme-mode');
  } catch (error) {
    stored = null;
  }
  var preferred = stored === 'dark' || stored === 'light'
    ? stored
    : (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
  document.documentElement.setAttribute('theme-mode', preferred);
  var meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', preferred === 'dark' ? '#101418' : '#f4f6f8');
})();

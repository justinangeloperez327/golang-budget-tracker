// Progressive enhancement only; native HTML forms and navigation still work.
(() => {
  const root = document.documentElement;
  const key = 'budget-tracker-theme';
  let saved;
  try { saved = window.localStorage.getItem(key); } catch { saved = null; }
  const apply = (value) => {
    root.dataset.theme = value === 'dark' ? 'dark' : 'light';
  };
  apply(saved === 'dark' || saved === 'light'
    ? saved
    : (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'));
  document.addEventListener('click', (event) => {
    const button = event.target.closest('[data-theme-toggle]');
    if (!button) return;
    const next = root.dataset.theme === 'dark' ? 'light' : 'dark';
    apply(next);
    try { window.localStorage.setItem(key, next); } catch { /* private mode */ }
  });
})();

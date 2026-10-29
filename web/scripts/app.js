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

// Enhanced financial submits preserve the original request_key after a network or
// server failure. No automatic retries: the user must deliberately submit again.
(() => {
  document.addEventListener('submit', async (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) ||
        form.method.toLowerCase() !== 'post' ||
        !form.querySelector('input[name="request_key"]') ||
        form.hasAttribute('hx-post') ||
        form.hasAttribute('hx-put')) return;
    event.preventDefault();
    if (form.dataset.saving === 'true') return;
    form.dataset.saving = 'true';

    let feedback = form.querySelector('[data-save-feedback]');
    if (!feedback) {
      feedback = document.createElement('p');
      feedback.setAttribute('role', 'status');
      feedback.setAttribute('aria-live', 'polite');
      feedback.dataset.saveFeedback = 'true';
      form.append(feedback);
    }
    feedback.textContent = 'Saving…';
    try {
      const body = new URLSearchParams(new FormData(form));
      if (event.submitter && event.submitter.name) {
        body.append(event.submitter.name, event.submitter.value);
      }
      const response = await fetch(form.action, {
        method: 'POST',
        credentials: 'same-origin',
        redirect: 'follow',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8' },
        body,
      });
      if (!response.ok) {
        feedback.textContent = 'Save not confirmed (' + response.status +
          '). Review your entries and retry using the same request.';
        return;
      }
      // A successful native POST uses 303 -> GET. Navigation only follows
      // an explicitly same-origin response. Never regenerate the request key.
      if (response.redirected) {
        const destination = new URL(response.url);
        if (destination.origin === window.location.origin) {
          window.location.assign(destination.href);
          return;
        }
      }
      feedback.textContent = 'Saved. Refresh the page to view the latest information.';
    } catch {
      feedback.textContent = 'Connection interrupted. Save status is unknown. ' +
        'Retry this form without refreshing to safely reuse the request key.';
    } finally {
      delete form.dataset.saving;
    }
  });
})();

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('source CSS uses Tailwind and scans templ components', async () => {
  const css = await readFile('web/styles/base.css','utf8');
  assert.match(css, /@import "tailwindcss"/);
  assert.match(css, /internal\/views.*\.templ/);
});
test('theme works without unsafe inline scripts', async () => {
  const js = await readFile('web/scripts/app.js','utf8');
  assert.match(js, /data-theme-toggle/);
  assert.match(js, /localStorage/);
});

test('save recovery rejects session expiry and keeps original idempotency key', async () => {
  const js = await readFile('web/scripts/app.js', 'utf8');
  assert.match(js, /finalURL.pathname === '\/login'/);
  assert.match(js, /new FormData\(form\)/);
  assert.match(js, /Session expired/);
  assert.match(js, /form.dataset.saving === 'true'/);
});

test('production runbook and manual restore safeguards are documented', async () => {
  const restore = await readFile('scripts/restore-postgres.sh', 'utf8');
  const backup = await readFile('scripts/backup-postgres.sh', 'utf8');
  const runbook = await readFile('docs/group-20-production.md', 'utf8');
  assert.match(restore, /YES_RESTORE_TO_EMPTY_DATABASE/);
  assert.match(restore, /--single-transaction/);
  assert.match(restore, /nspname='public'/);
  assert.match(backup, /umask 077/);
  assert.match(backup, /pg_restore --list/);
  assert.match(runbook, /No deploy, cloud resource provisioning/);
});

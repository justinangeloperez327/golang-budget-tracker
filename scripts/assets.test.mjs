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

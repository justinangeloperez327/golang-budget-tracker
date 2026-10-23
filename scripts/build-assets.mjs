import { copyFile, mkdir, stat } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import { join } from 'node:path';

const out = 'internal/assets';
await mkdir(out, { recursive: true });
const binary = join('node_modules', '.bin', process.platform === 'win32' ? 'tailwindcss.cmd' : 'tailwindcss');
const command = spawnSync(binary, ['-i', 'web/styles/base.css', '-o', join(out, 'app.css'), '--minify'], { stdio: 'inherit' });
if (command.error || command.status !== 0) {
  throw command.error ?? new Error('Tailwind CSS generation failed: ' + command.status);
}
await copyFile('node_modules/htmx.org/dist/htmx.min.js', join(out, 'htmx.min.js'));
await copyFile('web/scripts/app.js', join(out, 'app.js'));
for (const file of ['app.css', 'app.js', 'htmx.min.js']) {
  if ((await stat(join(out, file))).size < 100) throw new Error('Asset is unexpectedly empty: ' + file);
}
console.log('Built self-hosted Tailwind CSS, htmx and app.js.');

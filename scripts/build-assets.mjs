import { mkdir, copyFile } from 'node:fs/promises';

await mkdir('web/assets', { recursive: true });
await copyFile('web/styles/base.css', 'web/assets/app.css');
console.log('Group 1 placeholder stylesheet built.');

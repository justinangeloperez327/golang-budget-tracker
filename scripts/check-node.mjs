import { majorVersion } from './node-version.mjs';

const detected = majorVersion(process.version);
if (detected !== 24) {
  console.error('Node.js 24 is required for frontend assets; found ' + process.version);
  process.exitCode = 1;
} else {
  console.log('Node version OK: ' + process.version);
}

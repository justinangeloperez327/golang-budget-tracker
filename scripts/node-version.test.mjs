import test from 'node:test';
import assert from 'node:assert/strict';
import { majorVersion } from './node-version.mjs';

test('parses major Node versions', () => {
  assert.equal(majorVersion('v24.10.0'), 24);
  assert.equal(majorVersion('24.0.0'), 24);
  assert.equal(majorVersion('v22.16.0'), 22);
});
test('rejects malformed versions', () => {
  assert.throws(() => majorVersion('bad'), /Invalid Node version/);
});

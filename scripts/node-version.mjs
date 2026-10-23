export function majorVersion(version) {
  const match = /^v?(\d+)(?:\.|$)/.exec(version);
  if (!match) {
    throw new Error('Invalid Node version: ' + version);
  }
  return Number(match[1]);
}

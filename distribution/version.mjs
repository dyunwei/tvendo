import { readFileSync } from 'node:fs';
// SemVer without build metadata: accepted by npm, Docker tags and CLI asset URLs.
export const versionPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?$/;
export function readVersion() {
  const version = readFileSync(new URL('../VERSION', import.meta.url), 'utf8').trim();
  if (!versionPattern.test(version)) throw new Error('VERSION 必须为 SemVer，例如 0.1.0 或 0.2.0-beta.1');
  return version;
}
export const packageName = '@dyunwei/tvendo';
export const tarballName = version => `dyunwei-tvendo-${version}.tgz`;

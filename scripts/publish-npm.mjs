import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { execFileSync } from 'node:child_process';
import { packageName } from '../distribution/version.mjs';
import { verifyCLI } from './verify-cli.mjs';
export async function publicationStatus(version, integrity, fetcher = fetch) {
  const response = await fetcher(`https://registry.npmjs.org/${encodeURIComponent(packageName)}/${version}`, { signal: AbortSignal.timeout(30000) });
  if (response.status === 404) return 'new';
  if (!response.ok) throw new Error(`npm 版本查询失败（HTTP ${response.status}），停止发布`);
  const existing = await response.json();
  if (existing.dist?.integrity !== integrity) throw new Error('npm 已有同版本，但产物校验值不同；请核对发布产物，不可覆盖或跳过');
  return 'identical';
}
export function publishEnvironment(dryRun, source = process.env) {
  const env = { ...source };
  // An explicitly configured token must also work for the first package publish.
  // Do not attempt an OIDC exchange before that package has a trust binding.
  if (dryRun || env.NODE_AUTH_TOKEN?.trim()) {
    delete env.ACTIONS_ID_TOKEN_REQUEST_URL;
    delete env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
    delete env.NPM_ID_TOKEN;
  }
  if (dryRun) {
    env.NODE_AUTH_TOKEN = '';
    env.npm_config_update_notifier = 'false';
  }
  return env;
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const directory = resolve(process.argv[2] || 'distribution/dist');
  const manifest = verifyCLI(directory);
  const tarball = join(directory, manifest.package);
  const tag = manifest.version.includes('-') ? 'next' : 'latest';
  const dryRun = process.argv.includes('--dry-run');
  if (!dryRun) {
    const integrity = 'sha512-' + createHash('sha512').update(readFileSync(tarball)).digest('base64');
    if (await publicationStatus(manifest.version, integrity) === 'identical') {
      console.log(`npm ${packageName}@${manifest.version} 已存在且内容一致，跳过重复发布。`);
      process.exit(0);
    }
  }
  // Only this explicit command publishes. Packaging and smoke tests never contact publishing endpoints.
  execFileSync('npm', ['publish', tarball, '--access', 'public', '--tag', tag, '--registry', 'https://registry.npmjs.org/', ...(dryRun ? ['--dry-run', '--ignore-scripts', '--offline'] : [])], { stdio: 'inherit', env: publishEnvironment(dryRun) });
}

import { appendFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { readVersion } from '../distribution/version.mjs';
export function metadata(version, tag, prerelease) {
  if (tag !== `v${version}`) throw new Error(`Release 标签应为 v${version}，实际为 ${tag}`);
  const isPre = version.includes('-');
  if (prerelease !== isPre) throw new Error('GitHub Pre-release 标记与 VERSION 中的预发布后缀必须一致');
  return { version, tag, npm_tag: isPre ? 'next' : 'latest', image: `dyunwei/tvendo:${version}`, stable: String(!isPre) };
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const version = readVersion();
  if (process.argv.includes('--version')) console.log(version);
  else {
    const pre = process.env.RELEASE_PRERELEASE;
    if (pre !== 'true' && pre !== 'false') throw new Error('必须提供 RELEASE_PRERELEASE=true 或 false');
    const result = metadata(version, process.env.RELEASE_TAG, pre === 'true');
    if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, Object.entries(result).map(([k,v]) => `${k}=${v}\n`).join(''));
    console.log(JSON.stringify(result));
  }
}

import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { readVersion, packageName, tarballName } from '../distribution/version.mjs';
export function verifyCLI(directory, version = readVersion()) {
  const manifest = JSON.parse(readFileSync(join(directory, 'manifest.json')));
  if (manifest.version !== version || manifest.npm_package !== packageName || manifest.package !== tarballName(version)) throw new Error('CLI 清单中的版本或包名不符');
  const binaries = ['linux','darwin','windows'].flatMap(os => ['amd64','arm64'].map(arch => `tvendo-${os}-${arch}${os === 'windows' ? '.exe' : ''}`));
  if (JSON.stringify(manifest.platforms) !== JSON.stringify(binaries)) throw new Error('CLI 平台清单不符');
  const expected = new Set([...binaries, manifest.package, 'LICENSE', 'COPYING.GPL-3', 'CLI-README.md']);
  const checks = readFileSync(join(directory, 'SHA256SUMS'), 'utf8').trim().split('\n');
  for (const line of checks) {
    const match = /^([a-f0-9]{64})  ([A-Za-z0-9._-]+)$/.exec(line);
    if (!match || !expected.delete(match[2])) throw new Error('校验清单存在重复、未知或无效文件');
    if (createHash('sha256').update(readFileSync(join(directory, match[2]))).digest('hex') !== match[1]) throw new Error(`产物校验失败: ${match[2]}`);
  }
  if (expected.size) throw new Error('校验清单缺少文件');
  return manifest;
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const directory = resolve(process.argv[2] || 'distribution/dist');
  const manifest = verifyCLI(directory);
  console.log(`CLI ${manifest.version} 的版本、平台和校验清单验证通过。`);
}

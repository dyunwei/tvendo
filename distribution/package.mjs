// Package prebuilt binaries without install hooks or runtime downloads.
import { mkdirSync, readFileSync, writeFileSync, copyFileSync, chmodSync, mkdtempSync, rmSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { tmpdir } from 'node:os';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readVersion, versionPattern, packageName as npmName, tarballName } from './version.mjs';
const [outputArg, version = readVersion()] = process.argv.slice(2);
if (!outputArg || !versionPattern.test(version) || version !== readVersion()) throw new Error('用法: node distribution/package.mjs <输出目录> <版本>');
const output = resolve(outputArg);
const pkg = mkdtempSync(join(tmpdir(), 'tvendo-package-'));
mkdirSync(join(pkg, 'bin'), { recursive: true });
const checksums = [];
for (const os of ['linux', 'darwin', 'windows']) {
  for (const arch of ['amd64', 'arm64']) {
    const name = `tvendo-${os}-${arch}${os === 'windows' ? '.exe' : ''}`;
    const binary = readFileSync(join(output, name));
    copyFileSync(join(output, name), join(pkg, 'bin', name));
    chmodSync(join(pkg, 'bin', name), 0o755);
    checksums.push(`${createHash('sha256').update(binary).digest('hex')}  ${name}`);
  }
}
copyFileSync(new URL('./launcher.cjs', import.meta.url), join(pkg, 'launcher.cjs'));
chmodSync(join(pkg, 'launcher.cjs'), 0o755);
copyFileSync(new URL('./README.md', import.meta.url), join(pkg, 'README.md'));
copyFileSync(new URL('../LICENSE', import.meta.url), join(pkg, 'LICENSE'));
copyFileSync(new URL('../COPYING.GPL-3', import.meta.url), join(pkg, 'COPYING.GPL-3'));
writeFileSync(join(pkg, 'package.json'), JSON.stringify({
  name: npmName, version, description: 'Tvendo Agent 工具客户端',
  bin: { tvendo: 'launcher.cjs' }, files: ['launcher.cjs', 'bin', 'README.md', 'LICENSE', 'COPYING.GPL-3'],
  engines: { node: '>=18' }, os: ['linux', 'darwin', 'win32'], cpu: ['x64', 'arm64'],
  license: 'LGPL-3.0-only',
  homepage: 'https://github.com/dyunwei/tvendo',
  publishConfig: { access: 'public', registry: 'https://registry.npmjs.org/' },
  repository: { type: 'git', url: 'https://github.com/dyunwei/tvendo.git' }
}, null, 2));
execFileSync('npm', ['pack', '--ignore-scripts', '--cache', join(pkg, '.cache'), '--pack-destination', output], { cwd: pkg, stdio: 'inherit' });
rmSync(pkg, { recursive: true, force: true });
const packageName = tarballName(version);
checksums.push(`${createHash('sha256').update(readFileSync(join(output, packageName))).digest('hex')}  ${packageName}`);
for (const [source, name] of [[new URL('../LICENSE', import.meta.url), 'LICENSE'], [new URL('../COPYING.GPL-3', import.meta.url), 'COPYING.GPL-3'], [new URL('./README.md', import.meta.url), 'CLI-README.md']]) {
  copyFileSync(source, join(output, name));
  checksums.push(`${createHash('sha256').update(readFileSync(source)).digest('hex')}  ${name}`);
}
writeFileSync(join(output, 'SHA256SUMS'), checksums.join('\n') + '\n');
writeFileSync(join(output, 'manifest.json'), JSON.stringify({ version, npm_package: npmName, package: packageName, platforms: checksums.slice(0, 6).map(line => line.split('  ')[1]) }, null, 2) + '\n');

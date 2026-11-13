// Package prebuilt binaries without install hooks or runtime downloads.
import { mkdirSync, readFileSync, writeFileSync, copyFileSync, chmodSync, mkdtempSync, rmSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { tmpdir } from 'node:os';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
const [outputArg, version = '0.1.0'] = process.argv.slice(2);
if (!outputArg || !/^\d+\.\d+\.\d+$/.test(version)) throw new Error('用法: node distribution/package.mjs <输出目录> <版本>');
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
writeFileSync(join(pkg, 'package.json'), JSON.stringify({
  name: 'tvendo-cli', version, description: 'Tvendo Agent 工具客户端',
  bin: { tvendo: 'launcher.cjs' }, files: ['launcher.cjs', 'bin'],
  engines: { node: '>=18' }, os: ['linux', 'darwin', 'win32'], cpu: ['x64', 'arm64'],
  repository: { type: 'git', url: 'https://github.com/dyunwei/tvendo.git' }
}, null, 2));
execFileSync('npm', ['pack', '--ignore-scripts', '--cache', join(pkg, '.cache'), '--pack-destination', output], { cwd: pkg, stdio: 'inherit' });
rmSync(pkg, { recursive: true, force: true });
const packageName = `tvendo-cli-${version}.tgz`;
checksums.push(`${createHash('sha256').update(readFileSync(join(output, packageName))).digest('hex')}  ${packageName}`);
writeFileSync(join(output, 'SHA256SUMS'), checksums.join('\n') + '\n');
writeFileSync(join(output, 'manifest.json'), JSON.stringify({ version, package: packageName, platforms: checksums.slice(0, 6).map(line => line.split('  ')[1]) }, null, 2) + '\n');

#!/usr/bin/env node
'use strict';
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const os = { linux: 'linux', darwin: 'darwin', win32: 'windows' }[process.platform];
const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch];
if (!os || !arch) {
  console.error('此平台暂不支持 Tvendo CLI：' + process.platform + '/' + process.arch);
  process.exit(2);
}
const binary = path.join(__dirname, 'bin', `tvendo-${os}-${arch}${os === 'windows' ? '.exe' : ''}`);
const result = spawnSync(binary, process.argv.slice(2), { stdio: 'inherit', windowsHide: true });
if (result.error) { console.error('无法启动 Tvendo CLI，请检查安装包与文件执行权限。'); process.exit(1); }
if (result.signal) { process.kill(process.pid, result.signal); }
process.exit(result.status ?? 1);

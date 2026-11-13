// Exercise the packaged launcher, installer, checksum rejection, and agent flow.
import { createServer } from 'node:http';
import { readFileSync, mkdtempSync, existsSync, rmSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { spawn } from 'node:child_process';
import assert from 'node:assert/strict';
import { verifyCLI } from './verify-cli.mjs';
const dist = resolve(process.argv[2] || 'distribution/dist');
const manifest = verifyCLI(dist);
const temp = mkdtempSync(join(tmpdir(), 'tvendo-cli-smoke-'));
let tamper = false;
let calls = 0;
const server = createServer((req, res) => {
  const path = new URL(req.url, 'http://localhost').pathname;
  if (path.startsWith('/cli/')) {
    const name = path.slice(5);
    if (name === 'install.sh') {
      const template = readFileSync('backend/cmd/server/install.sh', 'utf8');
      res.end(template.replaceAll('{{SHELL_BASE}}', `'${base}'`).replaceAll('{{SHELL_VERSION}}', `'${manifest.version}'`));
    } else if (manifest.platforms.includes(name) || name === 'SHA256SUMS') {
      if (tamper && name === 'SHA256SUMS') res.end(readFileSync(join(dist, name), 'utf8').replaceAll(/^[a-f0-9]{64}/gm, '0'.repeat(64)));
      else res.end(readFileSync(join(dist, name)));
    } else { res.writeHead(404); res.end(); }
    return;
  }
  assert.equal(req.headers.authorization, 'Bearer tv_live_smoketest');
  res.setHeader('Content-Type', 'application/json');
  if (path === '/v1/info') res.end(JSON.stringify({ ok: true, data: { api_version: 1 } }));
  else if (path === '/v1/balance') res.end(JSON.stringify({ ok: true, data: { balance_micros: 1000 } }));
  else if (path === '/v1/catalog') res.end(JSON.stringify({ ok: true, data: { items: [{ tool_ref: 't1.b2s', name: '测试搜索' }] } }));
  else if (path === '/v1/run') {
    calls++;
    assert.ok(req.headers['x-request-id']);
    let body = ''; req.on('data', chunk => body += chunk); req.on('end', () => {
      const input = JSON.parse(body); assert.equal(input.tool_ref, 't1.b2s'); assert.equal(input.max_cost_micros, 10);
      res.end(JSON.stringify({ id: 42, cost_micros: 10, currency: 'CNY', data: { items: ['中文结果'] } }));
    });
  } else { res.writeHead(404); res.end('{}'); }
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const base = `http://127.0.0.1:${server.address().port}`;
const env = { ...process.env, TVENDO_CONFIG: join(temp, 'config.json'), TVENDO_SERVER: base, TVENDO_API_KEY: 'tv_live_smoketest', TVENDO_INSTALL_DIR: join(temp, 'installed'), npm_config_cache: join(temp, 'npm-cache') };
async function command(binary, args, expected = 0) {
  const child = spawn(binary, args, { env });
  let stdout = '', stderr = '';
  child.stdout.on('data', s => stdout += s); child.stderr.on('data', s => stderr += s);
  const code = await new Promise((resolve, reject) => { child.on('close', resolve); child.on('error', reject); });
  assert.equal(code, expected, stderr + stdout); return stdout;
}
try {
  const install = join(temp, 'install.sh');
  const { writeFileSync } = await import('node:fs');
  writeFileSync(install, await (await fetch(base + '/cli/install.sh')).text());
  await command('sh', [install, '--version', manifest.version]);
  const binary = join(env.TVENDO_INSTALL_DIR, 'tvendo');
  assert.equal((await command(binary, ['--version'])).trim(), manifest.version);
  assert.equal(JSON.parse(await command(binary, ['doctor', '--json'])).ok, true);
  const output = join(temp, 'result.json');
  const result = JSON.parse(await command(binary, ['run', 't1.b2s', '--input', '{"query":"测试"}', '--max-cost-micros', '10', '--output', output, '--json']));
  assert.equal(result.data.run_id, 42); assert.equal(result.data.currency, 'CNY'); assert.deepEqual(JSON.parse(readFileSync(output)), { items: ['中文结果'] });
  await command(binary, ['run', 't1.b2s', '--output', output, '--json'], 2);
  assert.equal(calls, 1);
  tamper = true;
  await command('sh', [install, '--dir', join(temp, 'bad')], 1);
  assert.equal(existsSync(join(temp, 'bad', 'tvendo')), false);
  const prefix = ['--yes', '--package', join(dist, manifest.package), 'tvendo'];
  assert.equal((await command('npx', [...prefix, '--version'])).trim(), manifest.version);
  const search = JSON.parse(await command('npx', [...prefix, 'tools', 'search', '搜索', '--json']));
  assert.equal(search.data.items[0].tool_ref, 't1.b2s');
  console.log('CLI 包、npx、本机安装、校验失败拦截及调用结果保存验证通过。');
} finally { server.close(); rmSync(temp, { recursive: true, force: true }); }

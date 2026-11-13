import test from 'node:test';
import assert from 'node:assert/strict';
import { metadata } from './release-metadata.mjs';
import { publicationStatus, publishEnvironment } from './publish-npm.mjs';
import { versionPattern } from '../distribution/version.mjs';
test('stable and prerelease tags are consistent across release channels', () => {
  assert.equal(metadata('0.1.0','v0.1.0',false).npm_tag,'latest');
  assert.equal(metadata('0.2.0-beta.1','v0.2.0-beta.1',true).npm_tag,'next');
  assert.throws(() => metadata('0.1.0','v0.1.1',false));
  assert.throws(() => metadata('0.2.0-beta.1','v0.2.0-beta.1',false));
  assert.throws(() => metadata('0.1.0','v0.1.0',true));
  for (const version of ['0.1.0','1.20.3','0.2.0-beta.1']) assert.ok(versionPattern.test(version));
  for (const version of ['01.0.0','0.1.0+build','0.1.0-01','0.1.0;echo']) assert.ok(!versionPattern.test(version));
});
test('only npm 404 means a new version; identical retries succeed and mismatches stop', async () => {
  const response = (status, integrity) => async () => ({ status, ok: status === 200, json: async () => ({dist:{integrity}}) });
  assert.equal(await publicationStatus('0.1.0','sha512-a',response(404)), 'new');
  assert.equal(await publicationStatus('0.1.0','sha512-a',response(200,'sha512-a')), 'identical');
  await assert.rejects(publicationStatus('0.1.0','sha512-a',response(200,'sha512-b')));
  await assert.rejects(publicationStatus('0.1.0','sha512-a',response(401)));
  await assert.rejects(publicationStatus('0.1.0','sha512-a',response(500)));
});

test('token publishing avoids OIDC while token-free publishing retains it', () => {
  const oidc = { ACTIONS_ID_TOKEN_REQUEST_URL: 'https://example.test/oidc', ACTIONS_ID_TOKEN_REQUEST_TOKEN: 'test-request-token' };
  const source = { ...oidc, NODE_AUTH_TOKEN: 'test-publish-token' };
  const token = publishEnvironment(false, source);
  assert.equal(token.NODE_AUTH_TOKEN, 'test-publish-token');
  assert.equal(token.ACTIONS_ID_TOKEN_REQUEST_URL, undefined);
  assert.equal(token.ACTIONS_ID_TOKEN_REQUEST_TOKEN, undefined);
  assert.equal(source.ACTIONS_ID_TOKEN_REQUEST_URL, oidc.ACTIONS_ID_TOKEN_REQUEST_URL);
  assert.equal(publishEnvironment(false, oidc).ACTIONS_ID_TOKEN_REQUEST_URL, oidc.ACTIONS_ID_TOKEN_REQUEST_URL);
  assert.equal(publishEnvironment(false, { ...oidc, NODE_AUTH_TOKEN: '' }).ACTIONS_ID_TOKEN_REQUEST_URL, oidc.ACTIONS_ID_TOKEN_REQUEST_URL);
  const dry = publishEnvironment(true, source);
  assert.equal(dry.NODE_AUTH_TOKEN, '');
  assert.equal(dry.ACTIONS_ID_TOKEN_REQUEST_URL, undefined);
});

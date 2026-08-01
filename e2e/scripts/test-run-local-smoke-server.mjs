#!/usr/bin/env node
/** Script-level regression test for normal exit, TERM, and original absence. */
import assert from 'node:assert/strict';
import { chmod, mkdir, mkdtemp, readFile, rm, unlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve, dirname } from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const scriptDir = dirname(fileURLToPath(import.meta.url));
const wrapper = resolve(scriptDir, 'run-local-smoke-server.mjs');
const root = await mkdtemp(join(tmpdir(), 'tokenmp-e2e-wrapper-test-'));
const webDir = join(root, 'apps/web');
await mkdir(webDir, { recursive: true });
const nextEnv = join(webDir, 'next-env.d.ts');
const tsconfig = join(webDir, 'tsconfig.json');
const mutator = join(root, 'mutate.mjs');

async function bytes(path) {
  return readFile(path);
}

async function writeFixture() {
  await writeFile(nextEnv, '/// <reference types="next" />\n');
  await writeFile(tsconfig, '{"compilerOptions":{"strict":true}}\n');
  return [await bytes(nextEnv), await bytes(tsconfig)];
}

await writeFile(mutator, `
import { writeFile } from 'node:fs/promises';
const [nextEnv, tsconfig, mode] = process.argv.slice(2);
await writeFile(nextEnv, 'generated next environment\\n');
await writeFile(tsconfig, '{"generated":true}\\n');
if (mode === 'wait') setInterval(() => {}, 1000);
`);
await chmod(mutator, 0o755);

function run(mode) {
  return spawn(process.execPath, [wrapper, process.execPath, mutator, nextEnv, tsconfig, mode], {
    env: { ...process.env, E2E_WEB_DIR: webDir },
    stdio: 'inherit',
  });
}

async function exits(child) {
  return new Promise((resolveExit) => child.once('exit', resolveExit));
}

try {
  let expected = await writeFixture();
  assert.equal(await exits(run('exit')), 0);
  assert.deepEqual(await bytes(nextEnv), expected[0]);
  assert.deepEqual(await bytes(tsconfig), expected[1]);

  expected = await writeFixture();
  const child = run('wait');
  for (let attempt = 0; attempt < 20; attempt += 1) {
    if ((await bytes(nextEnv)).toString().startsWith('generated')) break;
    await new Promise((resolveDelay) => setTimeout(resolveDelay, 50));
  }
  child.kill('SIGTERM');
  await exits(child);
  assert.deepEqual(await bytes(nextEnv), expected[0]);
  assert.deepEqual(await bytes(tsconfig), expected[1]);

  await unlink(nextEnv);
  await unlink(tsconfig);
  assert.equal(await exits(run('exit')), 0);
  await assert.rejects(bytes(nextEnv));
  await assert.rejects(bytes(tsconfig));

  console.log('run-local-smoke-server wrapper restoration: PASS');
} finally {
  await rm(root, { recursive: true, force: true });
}

#!/usr/bin/env node
/**
 * Start Next for local E2E smoke without allowing its generated TypeScript
 * configuration changes to persist in the developer worktree.
 *
 * Next runs as this wrapper's child so Playwright can terminate the complete
 * web-server process tree; all generated output stays in a disposable copy.
 */
import { copyFile, cp, mkdir, mkdtemp, readdir, rename, rm, symlink } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join, resolve } from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const command = process.argv.slice(2);
if (command.length === 0) {
  console.error(`usage: ${process.argv[1]} <server-command> [args ...]`);
  process.exit(64);
}

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const webDir = resolve(process.env.E2E_WEB_DIR ?? join(repoRoot, 'apps/web'));
const watchedNames = ['next-env.d.ts', 'tsconfig.json'];
const backupDir = await mkdtemp(join(tmpdir(), 'tokenmp-e2e-smoke-'));
const disposableWebDir = join(backupDir, 'apps/web');
const backups = new Map();
let child;
let stopping = false;
let cleaned = false;

async function snapshot() {
  for (const name of watchedNames) {
    const path = join(webDir, name);
    if (existsSync(path)) {
      const backup = join(backupDir, name);
      await copyFile(path, backup);
      backups.set(name, { exists: true, backup });
    } else {
      backups.set(name, { exists: false });
    }
  }
}

async function restoreFile(name, snapshot) {
  const target = join(webDir, name);
  if (!snapshot.exists) {
    await rm(target, { force: true });
    return;
  }

  const stage = join(dirname(target), `.${basename(target)}.e2e-restore-${process.pid}-${Date.now()}`);
  // The stage file lives beside the target, so rename is atomic on macOS/Linux.
  await copyFile(snapshot.backup, stage);
  await rename(stage, target);
}

async function cleanup() {
  if (cleaned) return;
  cleaned = true;
  const failures = [];
  for (const [name, snapshot] of backups) {
    try {
      await restoreFile(name, snapshot);
    } catch (error) {
      failures.push(`${name}: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
  await rm(backupDir, { recursive: true, force: true });
  if (failures.length) throw new Error(`failed to restore local-smoke source files: ${failures.join('; ')}`);
}

function terminateChild(signal) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  try {
    child.kill(signal);
  } catch (error) {
    if (error?.code !== 'ESRCH') throw error;
  }
}

async function stop(signal, exitCode) {
  if (stopping) return;
  stopping = true;
  terminateChild(signal);
  if (child && child.exitCode === null && child.signalCode === null) {
    // Next normally exits promptly. Escalate if it ignores TERM so cleanup is
    // not indefinitely delayed.
    const exited = await Promise.race([
      new Promise((done) => child.once('exit', done)),
      new Promise((done) => setTimeout(done, 5_000, 'timeout')),
    ]);
    if (exited === 'timeout') {
      terminateChild('SIGKILL');
      await new Promise((done) => child.once('exit', done));
    }
  }
  try {
    await cleanup();
    process.exitCode = exitCode;
  } catch (error) {
    console.error(error instanceof Error ? error.message : error);
    process.exitCode = 1;
  }
}

await snapshot();
// Keep Next's generated next-env.d.ts, tsconfig.json, and .next output outside
// the worktree. node_modules is reused by symlink, never copied.
await mkdir(disposableWebDir, { recursive: true });
for (const entry of await readdir(webDir)) {
  if (['node_modules', '.next', '.next-e2e'].includes(entry)) continue;
  await cp(join(webDir, entry), join(disposableWebDir, entry), { recursive: true });
}
await symlink(join(webDir, 'node_modules'), join(disposableWebDir, 'node_modules'));
child = spawn(command[0], command.slice(1), {
  cwd: disposableWebDir,
  detached: false,
  env: process.env,
  stdio: 'inherit',
});

for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
  process.on(signal, () => void stop(signal, 128 + ({ SIGINT: 2, SIGTERM: 15, SIGHUP: 1 })[signal]));
}

child.once('error', async (error) => {
  console.error(error.message);
  await stop('SIGTERM', 1);
});
child.once('exit', async (code, signal) => {
  if (stopping) return;
  stopping = true;
  try {
    await cleanup();
    process.exitCode = code ?? 1;
  } catch (error) {
    console.error(error instanceof Error ? error.message : error);
    process.exitCode = 1;
  }
});

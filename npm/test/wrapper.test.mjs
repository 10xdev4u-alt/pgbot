// Tests for the npm wrapper — the one piece of behaviour that lives outside Go.
// POSIX only (the fake binary is a shell script); CI runs it on ubuntu + macos,
// and the published-package npx smoke in release.yml covers Windows. Run with:
//   node --test npm/test/
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const shim = path.join(here, '..', 'pgbot', 'bin', 'pgbot.js');
const wrapperPkg = JSON.parse(fs.readFileSync(path.join(here, '..', 'pgbot', 'package.json'), 'utf8'));

// pkgFor / exeName are pure and exported — assert the mapping directly, including
// the least-exercised win32-arm64 path (DoD: verify it resolves correctly).
const mod = await import(pathToFileURL(shim).href);
test('platform → package/exe mapping (incl. win32-arm64)', () => {
  assert.equal(mod.pkgFor('linux', 'x64'), '@pgbot/linux-x64');
  assert.equal(mod.pkgFor('darwin', 'arm64'), '@pgbot/darwin-arm64');
  assert.equal(mod.pkgFor('win32', 'arm64'), '@pgbot/win32-arm64');
  assert.equal(mod.exeName('win32'), 'pgbot.exe');
  assert.equal(mod.exeName('linux'), 'pgbot');
  // The wrapper must ship an optionalDependency for every platform it can map to.
  for (const p of ['linux-x64', 'linux-arm64', 'darwin-x64', 'darwin-arm64', 'win32-x64', 'win32-arm64']) {
    assert.ok(wrapperPkg.optionalDependencies[`@pgbot/${p}`], `missing optionalDependency @pgbot/${p}`);
  }
});

test('no install scripts anywhere (npm ci --ignore-scripts is a no-op)', () => {
  assert.equal(wrapperPkg.scripts, undefined, 'wrapper must declare no scripts');
});

// The remaining tests spawn a fake binary; skip on Windows where a shell-script
// "binary" won't exec.
const posix = process.platform !== 'win32';

// stageFakeBinary builds a node_modules layout the shim can resolve: a fake
// @pgbot/<thisPlatform> whose bin/pgbot is a shell script we control, plus a copy
// of the real wrapper. Returns the dir to run the wrapper from.
function stageFakeBinary(script) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'pgbot-wrap-'));
  const key = `${process.platform}-${process.arch}`;
  const platDir = path.join(dir, 'node_modules', '@pgbot', key, 'bin');
  fs.mkdirSync(platDir, { recursive: true });
  fs.writeFileSync(path.join(platDir, 'pgbot'), script, { mode: 0o755 });
  fs.writeFileSync(
    path.join(dir, 'node_modules', '@pgbot', key, 'package.json'),
    JSON.stringify({ name: `@pgbot/${key}`, version: '0.0.0', os: [process.platform], cpu: [process.arch] })
  );
  const wrapDir = path.join(dir, 'node_modules', 'pgbot', 'bin');
  fs.mkdirSync(wrapDir, { recursive: true });
  fs.copyFileSync(shim, path.join(wrapDir, 'pgbot.js'));
  fs.writeFileSync(
    path.join(dir, 'node_modules', 'pgbot', 'package.json'),
    JSON.stringify({ name: 'pgbot', version: '0.0.0', bin: { pgbot: 'bin/pgbot.js' } })
  );
  return dir;
}

test('exit code and argv pass through verbatim (2 = critical)', { skip: !posix }, () => {
  const dir = stageFakeBinary('#!/bin/sh\nprintf "args:%s\\n" "$*"\nexit ${PGBOT_FAKE_EXIT:-0}\n');
  const wrapper = path.join(dir, 'node_modules', 'pgbot', 'bin', 'pgbot.js');
  const r = spawnSync(process.execPath, [wrapper, 'inspect', 'db', '--json'], {
    env: { ...process.env, PGBOT_FAKE_EXIT: '2' },
    encoding: 'utf8',
  });
  assert.equal(r.status, 2, 'a critical (2) exit must arrive as 2');
  assert.match(r.stdout, /args:inspect db --json/, 'argv must pass through verbatim');
});

test('clean exit is 0', { skip: !posix }, () => {
  const dir = stageFakeBinary('#!/bin/sh\nexit 0\n');
  const wrapper = path.join(dir, 'node_modules', 'pgbot', 'bin', 'pgbot.js');
  assert.equal(spawnSync(process.execPath, [wrapper], { encoding: 'utf8' }).status, 0);
});

test('missing platform binary → exit 64 with an actionable message', () => {
  // Run the shim from a scratch dir with no @pgbot package to resolve.
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'pgbot-none-'));
  const r = spawnSync(process.execPath, [shim, '--version'], { cwd: dir, encoding: 'utf8' });
  assert.equal(r.status, 64);
  assert.match(r.stderr, new RegExp(`${process.platform}-${process.arch}`), 'error names the platform');
  assert.match(r.stderr, /#install/, 'error points at the install docs');
});

test('SIGTERM is forwarded to the child', { skip: !posix }, async () => {
  const dir = stageFakeBinary(
    '#!/bin/sh\ntrap \'printf caught > "$PGBOT_SIGFILE"; exit 143\' TERM\ni=0\nwhile [ $i -lt 100 ]; do sleep 0.1; i=$((i+1)); done\n'
  );
  const wrapper = path.join(dir, 'node_modules', 'pgbot', 'bin', 'pgbot.js');
  const sigfile = path.join(dir, 'sig');
  const child = spawn(process.execPath, [wrapper], {
    env: { ...process.env, PGBOT_SIGFILE: sigfile },
    stdio: 'ignore',
  });
  await new Promise((r) => setTimeout(r, 400)); // let the fake install its trap
  child.kill('SIGTERM');
  await new Promise((resolve) => child.on('exit', resolve));
  assert.ok(fs.existsSync(sigfile), 'the child must receive the forwarded SIGTERM');
});

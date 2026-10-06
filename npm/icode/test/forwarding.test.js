'use strict';
// Self-contained forwarding test for the launcher. Pure node, no npm, no
// network. It builds a throwaway sandbox with dummy sibling packages so
// require.resolve succeeds, then injects a fake child_process.spawnSync so the
// exit-code / argv / stdio / signal contract is asserted without a real binary.
//
// Run with:  node npm/icode/test/forwarding.test.js

const assert = require('assert');
const fs = require('fs');
const os = require('os');
const path = require('path');
const cp = require('child_process');

const LAUNCHER_SRC = path.resolve(__dirname, '..', 'bin', 'icode.js');

const sandbox = fs.mkdtempSync(path.join(os.tmpdir(), 'icode-launcher-'));
const pkgSlug = 'icode-linux-x64';
fs.mkdirSync(path.join(sandbox, 'node_modules', pkgSlug), { recursive: true });
fs.writeFileSync(path.join(sandbox, 'node_modules', pkgSlug, 'package.json'),
  JSON.stringify({ name: pkgSlug, version: '0.0.0-test' }));
const launcherCopy = path.join(sandbox, 'icode.js');
fs.copyFileSync(LAUNCHER_SRC, launcherCopy);

// Fake spawn: capture the call, return a chosen result — never runs a real exe.
let captured = null;
let fakeResult = { status: 7, signal: null };
cp.spawnSync = (bin, args, opts) => { captured = { bin, args, opts }; return fakeResult; };

const L = require(launcherCopy);

console.log('launcher forwarding self-check (sandboxed, fake spawnSync)');

// 1. exit code forwarded verbatim, argv untouched, stdio inherited.
fakeResult = { status: 7, signal: null };
assert.strictEqual(L.run('linux', 'x64', ['chat', '-p', 'hi']), 7);
assert.deepStrictEqual(captured.args, ['chat', '-p', 'hi'], 'argv forwarded verbatim');
assert.strictEqual(captured.opts.stdio, 'inherit', 'stdio inherited (TUI needs a real tty)');
assert.ok(captured.bin.endsWith(path.join(pkgSlug, 'icode')), 'resolves linux/x64 binary: ' + captured.bin);
console.log('  ok  - exit code 7 forwarded; argv + stdio passed through');

// 2. child killed by a signal → 128+signum (shell convention), not a silent 0.
fakeResult = { status: null, signal: 'SIGTERM' };
const sigRc = L.run('linux', 'x64', []);
assert.strictEqual(sigRc, 128 + os.constants.signals.SIGTERM);
console.log('  ok  - SIGTERM from child -> ' + sigRc + ' (128+15)');

// 3. unsupported platform never reaches spawn.
captured = null;
assert.strictEqual(L.run('sunos', 'x64', []), 1);
assert.strictEqual(captured, null, 'must not spawn for an unsupported target');
console.log('  ok  - unsupported target exits 1 without spawning');

fs.rmSync(sandbox, { recursive: true, force: true });
console.log('\nforwarding checks passed');

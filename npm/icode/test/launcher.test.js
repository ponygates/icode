'use strict';
// Local self-check for the launcher's resolution logic. Pure node, no npm, no
// network. Run with:  node npm/icode/test/launcher.test.js
//
// Verifies that for every (platform, arch) pair the launcher picks the correct
// sibling package name and binary file name, and rejects everything else so the
// user gets the actionable error instead of a silent no-op.

const assert = require('assert');
const L = require('../bin/icode.js');

let n = 0;
const ok = (msg) => { n++; console.log('  ok  - ' + msg); };

// ── supported combos → package name + in-package binary name ──────────────
const table = [
  ['linux',   'x64',   'icode-linux-x64',   'icode'],
  ['linux',   'arm64', 'icode-linux-arm64', 'icode'],
  ['darwin',  'x64',   'icode-darwin-x64',  'icode'],
  ['darwin',  'arm64', 'icode-darwin-arm64','icode'],
  ['win32',   'x64',   'icode-win32-x64',   'icode.exe'],
  ['win32',   'arm64', 'icode-win32-arm64', 'icode.exe'],
];

console.log('launcher resolution self-check');
for (const [platform, arch, pkgName, binName] of table) {
  assert.strictEqual(L.resolvePackageName(platform, arch), pkgName,
    `package name for ${platform}/${arch}`);
  assert.strictEqual(L.resolveBinaryName(platform, arch), binName,
    `binary name for ${platform}/${arch}`);
  ok(`${platform}/${arch} -> ${pkgName} (${binName})`);
}

// ── unsupported combos must resolve to null (→ actionable error path) ──────
for (const [platform, arch] of [
  ['win32', 'ia32'], ['freebsd', 'x64'], ['linux', 'ppc64'], ['sunos', 'x64'],
]) {
  assert.strictEqual(L.resolveBinaryName(platform, arch), null,
    `unsupported ${platform}/${arch} must yield null binary name`);
  ok(`${platform}/${arch} correctly rejected`);
}

// ── unsupportedMessage mentions the failing target and a fallback ──────────
const msg = L.unsupportedMessage('win32', 'ia32');
assert.ok(msg.includes('win32-ia32'), 'error names the requested target');
assert.ok(msg.includes('install.sh'), 'error points at a fallback installer');
ok('unsupportedMessage is actionable (names target + install.sh fallback)');

// ── resolveBinaryPath throws when the sibling package is absent ────────────
// (the sibling packages are not installed in the repo checkout, so a real
// require.resolve must fail rather than return a bogus path — this proves the
// launcher does not fabricate paths.)
assert.throws(() => L.resolveBinaryPath('linux', 'x64'), /Cannot find module/,
  'missing sibling package surfaces as a module-not-found error');
ok('resolveBinaryPath refuses when the sibling package is not installed');

console.log(`\n${n} checks passed`);

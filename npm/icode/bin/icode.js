#!/usr/bin/env node
'use strict';
// iCode npm launcher — pure JS, zero runtime dependencies, never touches the
// network. It resolves the platform-specific optional dependency
// (icode-<platform>-<arch>), locates the bundled binary inside it and spawns it,
// forwarding argv and stdio and re-raising the child's exit code verbatim.
//
// Why the indirection: npm cannot install a per-OS binary into one package, so
// the published `icode` package is just this launcher plus a set of `os`/`cpu`
// gated sibling packages. Only the matching sibling is installed on a given
// machine; everything else is skipped by npm's platform filter.
//
// This file is imported by the release packaging AND executed as bin/icode, so
// the pure helpers are exported and the child only runs when invoked directly.

const path = require('path');
const { spawnSync } = require('child_process');

// Supported (process.platform, process.arch) pairs → binary asset extension.
// The keys mirror Node's own process.platform / process.arch values, which is
// what npm's os/cpu fields are matched against.
const SUPPORTED = {
  'linux-x64': '',
  'linux-arm64': '',
  'darwin-x64': '',
  'darwin-arm64': '',
  'win32-x64': '.exe',
  'win32-arm64': '.exe',
};

function platformKey(platform, arch) {
  return `${platform}-${arch}`;
}

// Returns the sibling npm package name for a platform/arch pair.
function resolvePackageName(platform, arch) {
  return `icode-${platformKey(platform, arch)}`;
}

// Returns the binary file name shipped inside the sibling package.
function resolveBinaryName(platform, arch) {
  const ext = SUPPORTED[platformKey(platform, arch)];
  return ext === undefined ? null : `icode${ext}`;
}

// Absolute path to the packaged binary, resolved through the sibling package's
// own package.json so we never hard-code node_modules layout (pnpm/yarn PnP).
function resolveBinaryPath(platform, arch) {
  const binName = resolveBinaryName(platform, arch);
  if (binName === null) return null;
  const pkg = resolvePackageName(platform, arch);
  // Throws MODULE_NOT_FOUND if the sibling was not installed for this platform;
  // callers translate that into the actionable error below.
  const pkgJson = require.resolve(`${pkg}/package.json`);
  return path.join(path.dirname(pkgJson), binName);
}

function unsupportedMessage(platform, arch) {
  const supported = Object.keys(SUPPORTED)
    .map((k) => '    ' + k)
    .join('\n');
  return (
    `icode: no prebuilt binary is available for ${platformKey(platform, arch)}.\n` +
    `Supported targets:\n${supported}\n\n` +
    `If you are on a supported OS this usually means the optional dependency ` +
    `'${resolvePackageName(platform, arch)}' was skipped during install.\n` +
    `Try reinstalling with a fresh lockfile, or install the static binary directly:\n` +
    `  curl -fsSL https://raw.githubusercontent.com/ponygates/icode/master/install.sh | bash\n`
  );
}

function run(platform, arch, argv) {
  const binPath = resolveBinaryPath(platform, arch);
  if (binPath === null) {
    process.stderr.write(unsupportedMessage(platform, arch));
    return 1;
  }
  const res = spawnSync(binPath, argv, { stdio: 'inherit' });
  if (res.error) {
    // spawnSync reports a missing/unusable binary as res.error rather than an
    // exit code — surface it instead of silently returning 0.
    process.stderr.write(`icode: failed to execute ${binPath}: ${res.error.message}\n`);
    return 1;
  }
  // Preserve the child's exit status; null means it was killed by a signal, so
  // re-raise the same signal (128+signum is the shell convention).
  if (typeof res.status === 'number') return res.status;
  if (res.signal) return 128 + require('os').constants.signals[res.signal];
  return 1;
}

if (require.main === module) {
  process.exitCode = run(process.platform, process.arch, process.argv.slice(2));
}

module.exports = {
  SUPPORTED,
  platformKey,
  resolvePackageName,
  resolveBinaryName,
  resolveBinaryPath,
  unsupportedMessage,
  run,
};

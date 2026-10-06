# icode (npm)

Platform launcher for [iCode](https://github.com/ponygates/icode), a multi-model
AI coding agent. Installing this package pulls in the matching prebuilt binary
for your OS/CPU via an `os`/`cpu`-gated optional dependency, so `icode` is on
your PATH right after `npm install -g icode`.

```sh
npm install -g icode
icode --version
```

## How it works

`bin/icode.js` is a tiny dependency-free launcher:

1. it maps `process.platform` + `process.arch` to a sibling package
   (`icode-linux-x64`, `icode-darwin-arm64`, `icode-win32-x64`, …);
2. it resolves that package's bundled binary;
3. it spawns the binary with your arguments and `stdio: "inherit"`, forwarding
   the exit code (or a fatal-signal status) back to the shell.

The launcher never touches the network and has no runtime dependencies.

## Supported targets

| package              | os      | cpu |
|----------------------|---------|-----|
| `icode-linux-x64`    | linux   | x64 |
| `icode-linux-arm64`  | linux   | arm64 |
| `icode-darwin-x64`   | darwin  | x64 (Intel) |
| `icode-darwin-arm64` | darwin  | arm64 (Apple silicon) |
| `icode-win32-x64`    | win32   | x64 |
| `icode-win32-arm64`  | win32   | arm64 |

If your platform is unsupported the launcher prints an actionable message and
points at the standalone installer (`install.sh`) or a build-from-source path.

## License

Apache-2.0.

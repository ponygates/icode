# winget channel (PonyGates.iCode)

`winget install PonyGates.iCode` is fed by **microsoft/winget-pkgs**, not by this
repository — the manifests under `PonyGates.iCode/0.1.0/` are the source copy we
keep here and PR upstream on every release.

## Layout (winget schema 1.6.0)

```
scripts/winget/
├── README.md                                ← this file
└── PonyGates.iCode/
    └── 0.1.0/
        ├── PonyGates.iCode.yaml                  version manifest
        ├── PonyGates.iCode.locale.en-US.yaml     default locale
        └── PonyGates.iCode.installer.yaml        installers + sha256
```

Three manifests per version, in a `<id>/<version>/` folder — that is exactly what
winget-pkgs expects (`manifest/P/PonyGates.iCode/0.1.0/…`). We ship a **fourth**,
an additional `locale.zh-CN.yaml` (`ManifestType: locale`) next to the default
`en-US` locale, because iCode ships a first-class 简体中文 UI.

## Generator (use this — do not hand-fill the hashes)

`scripts/gen_winget_manifest.sh` renders the whole 4-file set from a release's
`checksums.txt`, so `InstallerSha256` is always the *real* digest and every
channel (install.sh, scoop, homebrew, internal/update, winget) agrees on one:

```sh
scripts/gen_winget_manifest.sh gen    <version> <checksums.txt> [outdir]  # real hashes
scripts/gen_winget_manifest.sh check  <dir>      # structure + reject placeholder zeros
scripts/gen_winget_manifest.sh verify <dir> <checksums.txt>  # manifest ↔ release agree
```

CI's release job runs `gen`+`check`+`verify` against the freshly published
`checksums.txt`. The committed `0.1.0/` copy keeps all-zero hashes on purpose (a
fabricated digest is worse than an obvious placeholder); `check` fails on it, so
only CI-generated output — never the template — is what gets PR'd upstream.

## Per-release checklist

1. Tag the release (`vX.Y.Z`) and let CI publish the assets **plus
   `checksums.txt` and the `.minisig` signatures** (see
   `.github/workflows/build.yml`, release job).
2. Copy the folder and rename the version directory:
   `cp -r PonyGates.iCode/0.1.0 PonyGates.iCode/<new-version>` and update every
   `PackageVersion`, `InstallerUrl` tag and `ReleaseNotesUrl`.
3. **Replace the placeholder `InstallerSha256` values.** Never invent them — take
   them from the release's own `checksums.txt`:
   ```sh
   curl -fsSL https://github.com/ponygates/icode/releases/latest/download/checksums.txt \
     | grep 'icode-cli-windows-'
   ```
   or locally: `winget hash --file icode-cli-windows-amd64.exe`.
   (Prefer the checksum of the *signed* asset — verify the `.minisig` with
   `scripts/sign_release.sh verify <dir> scripts/signing/icode.pub` first.)
4. Validate the manifests locally:
   ```powershell
   winget validate .\PonyGates.iCode\0.1.0\
   ```
   or let the tooling generate them from scratch: `wingetcreate new https://github.com/ponygates/icode`
   (a.k.a. the `microsoft/wingetcreate` action `microsoft/wingetreleasesnew` in
   winget-pkgs) — the PR pipeline runs the same validation.
5. Open the PR against microsoft/winget-pkgs:
   ```sh
   gh repo fork microsoft/winget-pkgs --clone
   cd winget-pkgs
   mkdir -p manifest/P/PonyGates.iCode
   cp -r /e/icode/scripts/winget/PonyGates.iCode/0.1.0 manifest/P/PonyGates.iCode/
   git add manifest && git commit -m "Add PonyGates.iCode version 0.1.0" && git push
   gh pr create --repo microsoft/winget-pkgs
   ```
   Or in one command: `wingetcreate update PonyGates.iCode -v <version> -u <urls>`,
   which opens the PR for you.
6. After the PR merges (usually a few hours; the wingetbot re-indexes),
   `winget install PonyGates.iCode` works for users.

## Notes / policy

* We publish the **headless CLI** asset (`icode-cli-windows-*.exe`) only — the
  desktop GUI build is a WebView app and is distributed through the release page,
  Homebrew and Scoop channels.
* winget requires one manifest folder per version and rejects `ExternalInstaller`
  shims, so keep `InstallerType: portable` while the asset is a single exe. If
  iCode ever ships an MSIX/EXE installer, switch to `installerType: nullsoft` (or
  `msix`) and re-validate.
* The `0000…0000` sha in this repo is a deliberate placeholder: a fabricated
  checksum would pass validation locally and then fail (worse: silently accept a
  tampered asset) upstream.

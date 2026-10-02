#!/usr/bin/env bash
# ============================================================================
# iCode npm packaging — audit item 23 (npm distribution channel).
#
# The published `icode` package is a tiny JS launcher (npm/icode/bin/icode.js)
# plus six `os`/`cpu`-gated sibling packages, each holding the real binary. npm
# cannot ship a per-OS binary inside one package, so CI assembles them here from
# the release assets that .github/workflows/build.yml already produces.
#
# This script NEVER publishes. It only lays out the package tree and (optionally)
# runs `npm pack` to prove each tarball's structure is valid. Publishing lives in
# the workflow behind a secret + trigger guard.
#
# USAGE
#   scripts/gen_npm_packages.sh check
#       Run the launcher's pure-node resolution self-check (no npm needed).
#   scripts/gen_npm_packages.sh build <version> <release-dir> <out-dir> [--pack]
#       version : plain semver, NO leading v (e.g. 0.1.2 — CI passes ${TAG#v})
#       release : dir holding icode-cli-* assets (the CI `dist/` folder)
#       out     : destination dir for the assembled packages (created fresh)
#       --pack  : if npm is on PATH, `npm pack` each package to validate it
# ============================================================================
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLACEHOLDER="0.0.0-REPLACED_BY_CI"

# platform-slug → (release asset, in-package binary name)
# The slug is exactly what the launcher computes from process.platform/arch, so
# this table and npm/icode/bin/icode.js must stay in lockstep.
platforms=(
  "linux-x64:icode-cli-linux-amd64:icode"
  "linux-arm64:icode-cli-linux-arm64:icode"
  "darwin-x64:icode-cli-darwin-amd64:icode"
  "darwin-arm64:icode-cli-darwin-arm64:icode"
  "win32-x64:icode-cli-windows-amd64.exe:icode.exe"
  "win32-arm64:icode-cli-windows-arm64.exe:icode.exe"
)

die() { echo "gen_npm_packages: $*" >&2; exit 1; }

cmd_selfcheck() {
    command -v node >/dev/null 2>&1 || die "需要 node 才能跑 launcher 自检"
    echo "== launcher resolution self-check =="
    node "$REPO_ROOT/npm/icode/test/launcher.test.js"
    echo
    echo "== launcher forwarding self-check =="
    node "$REPO_ROOT/npm/icode/test/forwarding.test.js"
}

cmd_build() {
    local version="${1:-}" release_dir="${2:-}" out_dir="${3:-}"
    local do_pack="${4:-}"
    [ -n "$version" ] && [ -n "$release_dir" ] && [ -n "$out_dir" ] ||
        die "usage: build <version> <release-dir> <out-dir> [--pack]"
    case "$version" in
        v[0-9]*) die "version 不能带前导 v（CI 传 \${TAG#v}）：收到 '$version'" ;;
    esac
    [ -d "$release_dir" ] || die "release 目录不存在: $release_dir"

    # ── main launcher package ──────────────────────────────────────────────
    rm -rf "$out_dir"
    mkdir -p "$out_dir/icode"
    cp -r "$REPO_ROOT/npm/icode/." "$out_dir/icode/"
    # Rewrite the placeholder version in the main package.json + optionalDeps.
    sed -i "s/\"$PLACEHOLDER\"/\"$version\"/g" "$out_dir/icode/package.json"
    grep -q "\"version\": \"$version\"" "$out_dir/icode/package.json" ||
        die "主包版本号替换失败：模板占位符 '$PLACEHOLDER' 未生效"

    # ── one sibling package per platform ───────────────────────────────────
    local entry slug asset bin src dest
    for entry in "${platforms[@]}"; do
        IFS=':' read -r slug asset bin <<<"$entry"
        src="$release_dir/$asset"
        [ -f "$src" ] || die "缺少发布资产 $asset（在 $release_dir 下没找到）"
        dest="$out_dir/$slug"
        mkdir -p "$dest"
        cp "$REPO_ROOT/npm/platforms/$slug/package.json" "$dest/package.json"
        sed -i "s/\"$PLACEHOLDER\"/\"$version\"/g" "$dest/package.json"
        # Ship the binary under the fixed name the launcher expects.
        cp "$src" "$dest/$bin"
        case "$bin" in
            *.exe) chmod 644 "$dest/$bin" ;;   # Windows: no exec bit semantics
            *)     chmod 755 "$dest/$bin" ;;
        esac
        echo "built: $slug (from $asset -> $bin)"
    done

    echo
    echo "== assembled packages under $out_dir =="
    find "$out_dir" -maxdepth 2 -type f | sort

    # ── optional npm pack structure validation ─────────────────────────────
    if [ "$do_pack" = "--pack" ]; then
        if ! command -v npm >/dev/null 2>&1; then
            echo
            echo "NOTE: 本机 bash PATH 中没有 npm，跳过 --pack 结构校验。"
            echo "      CI 的 release-npm job 会装好 node/npm 后逐一 npm pack 验证。"
            return 0
        fi
        for d in "$out_dir"/icode "$out_dir"/*/; do
            [ -f "$d/package.json" ] || continue
            echo "== npm pack: $(basename "$d") =="
            ( cd "$d" && npm pack --contents . >/dev/null ) ||
                die "npm pack 校验失败: $d"
        done
        echo "npm pack 结构校验通过"
    fi
}

main() {
    case "${1:-}" in
        check) cmd_selfcheck ;;
        build) shift; cmd_build "$@" ;;
        *) sed -n '2,30p' "$0"; exit 1 ;;
    esac
}
main "$@"

#!/usr/bin/env bash
# ============================================================================
# iCode installer — Linux / macOS one-liner.
#
#   curl -fsSL https://raw.githubusercontent.com/ponygates/icode/master/install.sh | bash
#   curl -fsSL .../install.sh | bash -s -- --version v0.1.1
#   curl -fsSL .../install.sh | bash -s -- --bin-dir ~/.local/bin
#
# What it does (and does not) do:
#   * detects OS/arch, picks the matching headless CLI asset that CI publishes
#     (icode-cli-linux-amd64 / -linux-arm64 / -darwin-amd64 / -darwin-arm64);
#   * REFUSES to install unless the asset's sha256 matches the release's
#     checksums.txt — an unsigned CDN/redirect cannot swap the binary;
#   * additionally checks the minisign signature when the local `minisign`
#     binary is available and this build's public key is configured;
#   * installs to ~/.local/bin, falling back to /usr/local/bin (sudo if needed);
#   * never touches shell rc files, never runs as root unless /usr/local/bin
#     requires it, no telemetry.
#
# Everything network-facing goes through $DOWNLOAD so tests can point it at a
# local directory (ICODE_RELEASE_URL_BASE=file:///tmp/fake-release).
# ============================================================================
set -euo pipefail

REPO="${ICODE_REPO:-ponygates/icode}"
VERSION="${ICODE_VERSION:-latest}"
BIN_DIR="${ICODE_BIN_DIR:-}"
URL_BASE="${ICODE_RELEASE_URL_BASE:-}"
KEEP_TMP=0
WORK_DIR=""

# Public key material for signature verification. Placeholder by default — same
# fail-closed policy as the in-binary updater (internal/update/verify.go). When
# the maintainer commits scripts/signing/icode.pub, paste its base64 line here.
MINISIGN_PUBLIC_KEY="${ICODE_MINISIGN_PUBKEY:-REPLACE_WITH_BASE64_MINISIGN_PUBLIC_KEY}"

say() { printf '%s\n' "$*" >&2; }
die() { say "install.sh: $*"; exit 1; }

# Runs from the EXIT trap, i.e. after main() has returned — the work dir is a
# global precisely because a `local` would already be unset here under `set -u`.
cleanup() {
    if [ "$KEEP_TMP" -ne 1 ] && [ -n "$WORK_DIR" ] && [ -d "$WORK_DIR" ]; then
        rm -rf "$WORK_DIR"
    fi
}

usage() {
    cat <<'EOF'
用法: install.sh [--version vX.Y.Z] [--bin-dir DIR] [--keep-tmp]
  --version  指定发布版本（默认 latest）
  --bin-dir  安装目录（默认 ~/.local/bin，其次 /usr/local/bin）
  --keep-tmp 保留临时文件（调试用）
EOF
}

# ── pure helpers (unit-testable without network) ───────────────────────────

# detect_os: normalized GOOS for this machine.
detect_os() {
    local kernel
    kernel="$(uname -s 2>/dev/null || echo unknown)"
    case "$kernel" in
        Linux) echo linux ;;
        Darwin) echo darwin ;;
        *) die "不支持的操作系统 '$kernel'（Windows 请用 install.bat / scoop / winget）" ;;
    esac
}

# detect_arch: normalized GOARCH for this machine.
detect_arch() {
    local machine
    machine="$(uname -m 2>/dev/null || echo unknown)"
    case "$machine" in
        x86_64 | amd64) echo amd64 ;;
        arm64 | aarch64) echo arm64 ;;
        *) die "不支持的 CPU 架构 '$machine'" ;;
    esac
}

# asset_name: the release asset for an os/arch pair.
asset_name() { echo "icode-cli-$1-$2"; }

# release_base: download root for a version ('latest' uses GitHub's redirect).
release_base() {
    if [ -n "$URL_BASE" ]; then
        echo "${URL_BASE%/}"
    elif [ "$VERSION" = "latest" ]; then
        echo "https://github.com/$REPO/releases/latest/download"
    else
        echo "https://github.com/$REPO/releases/download/$VERSION"
    fi
}

# checksum_for: prints the expected sha256 for $1 from a checksums.txt body on
# stdin; non-zero exit means the asset is not listed (caller must refuse).
checksum_for() {
    awk -v n="$1" '
        {
            digest = $1
            if (length(digest) != 64 || digest !~ /^[0-9a-fA-F]+$/) next
            name = $2
            sub(/^[*]/, "", name)
            if (name == n) { print digest; found = 1; exit }
        }
        END { if (!found) exit 1 }
    '
}

# sha256_of: prints the sha256 hex digest of a file (GNU or BSD tooling).
sha256_of() {
    local f="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$f" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$f" | awk '{print $1}'
    else
        die "需要 sha256sum 或 shasum 才能校验下载文件"
    fi
}

# verify_sha256: refuses to continue unless file matches the digest.
verify_sha256() {
    local file="$1" want="$2" got
    got="$(sha256_of "$file")"
    if [ "${#want}" -ne 64 ]; then
        die "checksums.txt 里没有预期的 sha256，拒绝安装"
    fi
    if [ "$(printf '%s' "$got" | tr 'A-F' 'a-f')" != "$(printf '%s' "$want" | tr 'A-F' 'a-f')" ]; then
        die "sha256 校验失败：期望 $want，实际 $got —— 已拒绝安装（下载被篡改或资产与清单不匹配）"
    fi
    say "sha256 校验通过: $got"
}

# pick_bin_dir: ~/.local/bin when usable, else /usr/local/bin (sudo if needed).
pick_bin_dir() {
    if [ -n "$BIN_DIR" ]; then
        echo "$BIN_DIR"
        return 0
    fi
    local home user_local="/usr/local/bin"
    home="${HOME:-}"
    if [ -n "$home" ]; then
        user_local="$home/.local/bin"
    fi
    if mkdir -p "$user_local" 2>/dev/null && [ -w "$user_local" ]; then
        echo "$user_local"
        return 0
    fi
    echo "/usr/local/bin"
}

# ── network ────────────────────────────────────────────────────────────────

# fetch: DOWNLOAD <url> <dest>
fetch() {
    local url="$1" dest="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --retry 3 --retry-delay 1 -o "$dest" "$url"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$dest" "$url"
    else
        die "需要 curl 或 wget 以下载发布资产"
    fi
}

# verify_signature: minisign check. With no key baked in it prints a notice and
# relies on sha256 only; once scripts/signing/icode.pub is committed (paste its
# base64 into MINISIGN_PUBLIC_KEY) verification becomes mandatory, matching the
# fail-closed policy of internal/update/verify.go.
verify_signature() {
    local file="$1" asset="$2" sigfile pubfile
    if [ "${MINISIGN_PUBLIC_KEY#REPLACE_WITH}" = "$MINISIGN_PUBLIC_KEY" ]; then
        # key is configured (does NOT start with the placeholder prefix)
        :
    else
        say "警告: 此安装脚本尚未内置发布公钥，跳过 minisign 校验（仅 sha256）。"
        return 0
    fi
    command -v minisign >/dev/null 2>&1 ||
        die "已内置发布公钥但本机没有 minisign，无法校验签名；请安装 minisign 后重试"
    sigfile="$file.minisig"
    pubfile="$file.pub"
    fetch "$(release_base)/$asset.minisig" "$sigfile" ||
        { rm -f "$sigfile"; die "发布产物缺少 $asset.minisig，拒绝安装"; }
    printf 'untrusted comment: iCode release key\n%s\n' "$MINISIGN_PUBLIC_KEY" >"$pubfile"
    if minisign -V -q -m "$file" -x "$sigfile" -p "$pubfile"; then
        say "minisign 签名校验通过。"
    else
        die "minisign 签名校验失败：$asset 不是由 iCode 官方私钥发布的"
    fi
    rm -f "$sigfile" "$pubfile"
}

# ── main ───────────────────────────────────────────────────────────────────

main() {
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --version)
                [ "$#" -ge 2 ] || die "--version 需要一个参数"
                VERSION="$2"
                shift 2
                ;;
            --bin-dir)
                [ "$#" -ge 2 ] || die "--bin-dir 需要一个参数"
                BIN_DIR="$2"
                shift 2
                ;;
            --keep-tmp) KEEP_TMP=1; shift ;;
            -h | --help) usage; exit 0 ;;
            *) die "未知参数 '$1'（--help 查看用法）" ;;
        esac
    done

    local os arch asset base tmp sum
    os="$(detect_os)"
    arch="$(detect_arch)"
    asset="$(asset_name "$os" "$arch")"
    base="$(release_base)"
    WORK_DIR="$(mktemp -d)"
    tmp="$WORK_DIR/$asset"

    trap cleanup EXIT

    say "==> 平台 ${os}/${arch}，资产 ${asset}（${VERSION}）"
    say "==> 下载 ${base}/checksums.txt"
    if ! fetch "$base/checksums.txt" "$WORK_DIR/checksums.txt"; then
        die "无法获取 checksums.txt —— 该发布未附带校验清单，拒绝安装未校验的二进制"
    fi
    sum="$(checksum_for "$asset" <"$WORK_DIR/checksums.txt" || true)"
    [ -n "$sum" ] || die "checksums.txt 中没有 $asset 的条目，拒绝安装"

    say "==> 下载 ${base}/${asset}"
    fetch "$base/$asset" "$tmp"
    verify_sha256 "$tmp" "$sum"
    verify_signature "$tmp" "$asset"

    chmod 755 "$tmp"

    local dest_dir dest
    dest_dir="$(pick_bin_dir)"
    mkdir -p "$dest_dir" 2>/dev/null || true
    if [ -w "$dest_dir" ]; then
        mv -f "$tmp" "$dest_dir/icode"
    elif command -v sudo >/dev/null 2>&1; then
        say "==> 需要写权限，使用 sudo 安装到 $dest_dir"
        sudo mv -f "$tmp" "$dest_dir/icode"
    else
        die "$dest_dir 不可写且没有 sudo；请用 --bin-dir 指定可写目录"
    fi
    dest="$dest_dir/icode"

    local ver="unknown"
    if ver_out="$("$dest" --version 2>&1 | head -1)"; then
        ver="$ver_out"
    fi
    say "==> 已安装: $dest"
    say "==> 版本:   $ver"
    case ":$PATH:" in
        *":$dest_dir:"*) ;;
        *) say "note: $dest_dir 不在 PATH 中，请加入：export PATH=\"$dest_dir:\$PATH\"" ;;
    esac
    say "运行 'icode --help' 开始使用。"
}

# Only execute when run directly, so the pure helpers can be sourced in tests.
if [ "${BASH_SOURCE:-$0}" = "$0" ]; then
    main "$@"
fi

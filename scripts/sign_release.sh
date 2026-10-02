#!/usr/bin/env bash
# ============================================================================
# iCode release signing — minisign (audit item: releases were unsigned).
#
# WHAT THIS DOES
#   Produces a detached <asset>.minisig for every release asset, plus a
#   checksums.txt (sha256) that the updater also verifies. GitHub Actions runs
#   `sign` during the release job; the maintainer runs `keygen` exactly once.
#
# KEY PAIR — READ THIS ONCE, IT IS THE WHOLE SECURITY MODEL
#   minisign -G -p icode.pub -s icode.sec        #交互式设置口令
#   * icode.pub  PUBLIC key  → committed. Either paste its base64 payload into
#     internal/update/verify.go (MinisignPublicKey) or drop the file into the
#     repo at scripts/signing/icode.pub — the release job injects the base64
#     with -ldflags -X so the shipped binary carries it.
#   * icode.sec  PRIVATE key → NEVER COMMITTED. Not to git, not to an artifact,
#     not to a chat log. Store it only as the repository secret
#     MINISIGN_SECRET_KEY (base64 of the file, `base64 -w0 icode.sec`).
#     The key must have an EMPTY passphrase: minisign reads a passphrase from
#     the tty only, so a passphrase-protected key cannot be used by the
#     unattended release job (scripts/sign_release.sh sign refuses).
#     Keep an offline copy too — losing it means every user's updater starts
#     rejecting your releases.
#   Add *.sec to .gitignore (done) and keep working copies outside the repo,
#   e.g. ~/.config/icode-signing/.
#
# SIGNATURE FORMAT CONTRACT
#   Use minisign's DEFAULT ("Ed", non-hashed) mode. Do NOT pass --hashed: that
#   emits "ED" signatures over a Blake2b-512 digest, and internal/update verifies
#   with stdlib crypto/ed25519 only (no Blake2b), so it rejects them on purpose.
#
# USAGE
#   scripts/sign_release.sh keygen [outdir]
#   scripts/sign_release.sh pub    <secret-key>        # print the public key
#   scripts/sign_release.sh sign   <dir> <secret-key> [password]
#   scripts/sign_release.sh verify <dir> <pubkey-file>
#   scripts/sign_release.sh embed  <pubkey-file>       # Go-constant ready to paste
# ============================================================================
set -euo pipefail

die() { echo "sign_release: $*" >&2; exit 1; }

need_minisign() {
    command -v minisign >/dev/null 2>&1 && return 0
    cat >&2 <<'EOF'
sign_release: 需要 minisign 可执行文件。
  Debian/Ubuntu: sudo apt-get install minisign
  macOS:         brew install minisign
  Windows:       scoop install minisign   (或 chocolatey: choco install minisign)
  源码:          https://github.com/jedisct1/minisign
EOF
    exit 1
}

# Only assets that users can execute need signatures; checksums.txt itself is
# signed too, so a tampered checksum list cannot slip through.
is_asset() {
    case "$(basename "$1")" in
        icode-cli-* | icode-desktop-* | checksums.txt) return 0 ;;
        *.minisig) return 1 ;;
        *) return 1 ;;
    esac
}

cmd="${1:-}"; shift || true
case "$cmd" in
    keygen)
        need_minisign
        outdir="${1:-.}"
        [ -e "$outdir/icode.sec" ] && die "$outdir/icode.sec 已存在， refusing to overwrite a live private key"
        minisign -G -p "$outdir/icode.pub" -s "$outdir/icode.sec"
        chmod 600 "$outdir/icode.sec" 2>/dev/null || true
        echo
        echo "Public key  (COMMIT this):        $outdir/icode.pub"
        echo "Private key (NEVER COMMIT):       $outdir/icode.sec"
        echo "CI secret value:                  base64 -w0 $outdir/icode.sec"
        echo "Go constant line:                 $(bash "$(dirname "$0")/sign_release.sh" embed "$outdir/icode.pub" | tail -1)"
        echo
        echo "提示: CI 需要空口令私钥（minisign -G 时口令留空两次回车），"
        echo "      因为 minisign 只从终端读取口令，无法在 Actions 里非交互输入。"
        ;;

    pub)
        need_minisign
        pubf="${1:?usage: pub <pubkey-file>}"
        cat "$pubf"
        ;;

    sign)
        need_minisign
        dir="${1:?usage: sign <dir> <secret-key> [password]}"
        sec="${2:?usage: sign <dir> <secret-key> [password]}"
        pw="${3:-}"
        # minisign reads its passphrase from the terminal, never from a pipe, so
        # an unattended run needs a key with an EMPTY passphrase (minisign -G and
        # just press Enter twice). Do not "work around" that by leaking a pty.
        if [ -n "$pw" ]; then
            die "minisign 无法非交互式输入口令。请用空口令私钥配置 MINISIGN_SECRET_KEY，" \
                "或在本地手动签名后 gh release upload <tag> <asset>.minisig"
        fi
        shopt -s nullglob
        n=0
        for f in "$dir"/*; do
            is_asset "$f" || continue
            minisign -S -W -m "$f" -s "$sec"
            n=$((n + 1))
            echo "signed: $(basename "$f").minisig"
        done
        [ "$n" -gt 0 ] || die "no release assets found in $dir"
        echo "signed $n asset(s)"
        ;;

    verify)
        need_minisign
        dir="${1:?usage: verify <dir> <pubkey-file>}"
        pub="${2:?usage: verify <dir> <pubkey-file>}"
        shopt -s nullglob
        for f in "$dir"/*; do
            is_asset "$f" || continue
            [ -e "$f.minisig" ] || die "missing signature for $(basename "$f")"
            minisign -V -q -m "$f" -x "$f.minisig" -p "$pub"
            echo "verified: $(basename "$f")"
        done
        ;;

    embed)
        # Emit the single base64 line that goes into MinisignPublicKey.
        pub="${1:?usage: embed <pubkey-file>}"
        line="$(grep -v '^untrusted comment:' "$pub" | tr -d '\r\n[:space:]')"
        [ -n "$line" ] || die "no base64 payload in $pub"
        echo "// 由 scripts/sign_release.sh embed 生成，勿手工编辑。"
        echo "MinisignPublicKey = \"$line\""
        ;;

    *)
        sed -n '2,40p' "$0"
        exit 1
        ;;
esac

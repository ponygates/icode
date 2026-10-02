#!/usr/bin/env bash
# ============================================================================
# iCode coverage gate — real pass/fail enforcement over a Go cover profile.
#
# Usage:
#   scripts/check_coverage.sh [coverage.out]
#
# What it enforces (all thresholds are *statement* coverage percentages):
#   1. TOTAL_MIN   — overall coverage across every package in the profile.
#                    Fails the build if the project as a whole regresses.
#   2. GUARD_PKGS  — per-package floors for the packages that must not silently
#                    rot even when the total holds (the agent loop and the tool
#                    registry are where a regression actually ships bugs). A
#                    package listed in GUARD_PKGS that is *missing* from the
#                    profile (build failure, tests deleted, package renamed) is
#                    treated as 0% and fails too — that is the whole point.
#
# Thresholds were measured on 2026-09 with:
#   go test ./... -coverprofile=coverage.out
#   go tool cover -func=coverage.out | tail -1      -> total: 40.5%
#   internal/core/conversation                      -> 50.3%
#   internal/core/tool                              -> 45.0%
# Floors sit ~5 points under the measured values so ordinary work does not
# instantly break; RAISE THEM when coverage improves.
#
# Exit codes: 0 = gate passed, 1 = gate failed, 2 = bad input (no profile).
# ============================================================================
set -euo pipefail

PROFILE="${1:-coverage.out}"
TOTAL_MIN="${TOTAL_MIN:-35}"
# package=floor pairs, space separated. Package paths are as they appear in the
# cover profile with the module prefix stripped (no trailing slash).
GUARD_PKGS="${GUARD_PKGS:-internal/core/conversation=45 internal/core/tool=40}"

if [ ! -s "$PROFILE" ]; then
    echo "coverage gate: profile '$PROFILE' is missing or empty (test build failed?)" >&2
    exit 2
fi
if ! head -1 "$PROFILE" | grep -q '^mode:'; then
    echo "coverage gate: '$PROFILE' is not a Go coverage profile" >&2
    exit 2
fi

# ── Per-package + total coverage, straight from the profile ───────────────
# Profile lines look like:  <module>/internal/core/tool/edit.go:12.34,15.3 2 1
# Field 1 = file:block range, field 2 = #statements, field 3 = hit count.
REPORT="$(awk '
    NR == 1 && /^mode:/ { next }
    {
        split($1, a, ":"); dir = a[1]
        sub(/\.go$/, "", dir)            # internal/core/tool/edit.go -> .../edit
        sub(/[^\/]*$/, "", dir)          # -> internal/core/tool/
        sub(/^github\.com\/ponygates\/icode\//, "", dir)
        sub(/\/$/, "", dir)              # -> internal/core/tool
        if (dir == "") dir = "."         # package at the module root
        n = NF
        tot[dir]   += $2
        if ($n > 0) cov[dir] += $2
        T += $2
        if ($n > 0) C += $2
    }
    END {
        printf "TOTAL %.2f %d\n", (T > 0 ? 100 * C / T : 0), T
        for (p in tot) printf "%s %.2f %d\n", p, (tot[p] > 0 ? 100 * cov[p] / tot[p] : 0), tot[p]
    }
' "$PROFILE" | sort)"

TOTAL="$(printf '%s\n' "$REPORT" | awk '$1 == "TOTAL" { print $2 }')"
TOTAL_STMTS="$(printf '%s\n' "$REPORT" | awk '$1 == "TOTAL" { print $3 }')"

pkg_pct() { printf '%s\n' "$REPORT" | awk -v p="$1" '$1 == p { print $2 }'; }

echo "──────────────── iCode coverage gate ────────────────"
echo "total coverage : ${TOTAL}% over ${TOTAL_STMTS} statements (floor ${TOTAL_MIN}%)"

FAIL=0
fail() {
    echo "FAIL: $*" >&2
    FAIL=1
}

if awk -v a="$TOTAL" -v b="$TOTAL_MIN" 'BEGIN { exit !(a + 0 < b + 0) }'; then
    fail "total coverage ${TOTAL}% is below the ${TOTAL_MIN}% floor (regression in the project as a whole)"
fi

echo "──────── guard packages (per-package floors) ────────"
for pair in $GUARD_PKGS; do
    pkg="${pair%%=*}"
    floor="${pair#*=}"
    pct="$(pkg_pct "$pkg")"
    if [ -z "$pct" ]; then
        fail "$pkg has NO coverage data in the profile — its tests stopped running (floor ${floor}%)"
        continue
    fi
    if awk -v a="$pct" -v b="$floor" 'BEGIN { exit !(a + 0 < b + 0) }'; then
        fail "$pkg coverage ${pct}% is below its ${floor}% floor"
    else
        echo "ok   : $pkg ${pct}% (floor ${floor}%)"
    fi
done

# Full per-package table for PR review (top + bottom of the distribution).
echo "──────── per-package coverage (all packages) ─────────"
printf '%s\n' "$REPORT" | awk '$1 != "TOTAL" { printf "%-46s %6s%%  (%s stmts)\n", $1, $2, $3 }' | sort -k2 -n

if [ "$FAIL" -ne 0 ]; then
    echo "coverage gate: FAILED — raise coverage or justify a threshold change in scripts/check_coverage.sh" >&2
    exit 1
fi
echo "coverage gate: PASSED"

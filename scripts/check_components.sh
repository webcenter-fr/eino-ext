#!/usr/bin/env bash
set -euo pipefail

# check_components.sh — verify that every component package has a test file and README.
#
# Walk each directory under components/*/* that contains a non-test .go file
# (i.e. source files), and ensure a _test.go file AND a README.md are present.
# Prints missing items to stderr. Exits non-zero if any package is incomplete.
#
# Exclusions:
#   - Directories containing only test helpers (no non-test .go files) are skipped
#     (they serve the parent package).
#   - libs/ are not enforced for README (test file only).
#
# Flags:
#   --strict    also require README.md for libs/ packages (default: test-only for libs/).

STRICT=false
if [[ "${1:-}" == "--strict" ]]; then
    STRICT=true
fi

MISSING=0

check_dir() {
    local dir="$1"
    local require_readme="$2"

    local has_src=false
    local has_test=false
    local has_readme=false

    for f in "$dir"/*; do
        [[ -f "$f" ]] || continue
        local name="${f##*/}"
        case "$name" in
            *_test.go) has_test=true ;;
            *.go)       has_src=true ;;
            README.md)  has_readme=true ;;
        esac
    done

    if ! $has_src; then
        return 0
    fi

    if ! $has_test; then
        echo "MISSING _test.go: $dir" >&2
        MISSING=$((MISSING + 1))
    fi

    if $require_readme && ! $has_readme; then
        echo "MISSING README.md: $dir" >&2
        MISSING=$((MISSING + 1))
    fi
}

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Components: require both test and README.
if [[ -d "$ROOT/components" ]]; then
    for dir in "$ROOT"/components/*/*/; do
        [[ -d "$dir" ]] || continue
        # Skip go.sum / go.mod / .md / hidden dirs that match the glob.
        [[ "$dir" != *"/.git/"* ]] || continue
        check_dir "$dir" true
    done
fi

# libs/: require test; README is recommended but not enforced unless --strict.
if [[ -d "$ROOT/libs" ]]; then
    for dir in "$ROOT"/libs/*/; do
        [[ -d "$dir" ]] || continue
        check_dir "$dir" $STRICT
    done
fi

if [[ $MISSING -gt 0 ]]; then
    echo "$MISSING item(s) missing" >&2
    exit 1
fi

echo "All component packages have required files."

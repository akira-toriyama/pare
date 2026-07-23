#!/bin/sh
# check-docs.sh — README hygiene guard. One robust, low-false-positive invariant:
#   README.md must not hardcode a release version (v1.2.3 / 1.2.3) — docs link to
#   the Releases page instead, so they never rot as versions advance. (Two-part
#   versions like Go's 1.25 are allowed.)
# There is no README.ja.md: canonical docs are English-only (see the fleet
# doc-consistency policy in akira-toriyama/.github), so the old EN/JA
# mutual-link check is retired with it. Deeper prose review is left to review;
# this catches the staleness trap the 2026-07-03 audit flagged (F11).
set -eu
cd "$(dirname "$0")/.."
fail=0

if [ ! -f README.md ]; then
  echo "✖ missing README.md"
  fail=1
elif hits=$(grep -nE '\bv?[0-9]+\.[0-9]+\.[0-9]+\b' README.md); then
  echo "✖ README.md hardcodes a release version (keep docs version-agnostic — link to Releases):"
  echo "$hits"
  fail=1
fi

[ "$fail" -eq 0 ] && echo "✓ docs guard passed"
exit "$fail"

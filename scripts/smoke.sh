#!/bin/sh
# smoke.sh — the live checks run against a built pare binary, shared by
# scripts/check.sh and the CI `extras` job (both OS legs) so the two cannot
# drift: version / passthrough / a buried error survives a small budget.
# Usage: sh scripts/smoke.sh <path-to-pare-binary>
set -eu
BIN="$1"

"$BIN" version
# small output passes through untouched
printf 'a\nb\nc\n' | "$BIN" | grep -qx b
# a buried error survives a small budget that a blind tail would drop
{ seq 1 200; echo "ERROR: boom"; seq 201 400; } \
  | "$BIN" --budget-bytes 300 --head 3 --tail 3 | grep -q 'ERROR: boom'

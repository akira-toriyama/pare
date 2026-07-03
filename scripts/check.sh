#!/bin/sh
# check.sh — the full local verification, runnable by you or by Claude Code with
# no TTY. Mirrors what .github/workflows/build.yml enforces in CI, so a green run
# here means a green CI.
set -eu
cd "$(dirname "$0")/.."
export GOTOOLCHAIN=local

echo "→ go build"
go build ./...

echo "→ go vet"
go vet ./...

echo "→ go test -race"
go test -race ./...

if command -v golangci-lint >/dev/null 2>&1; then
  echo "→ golangci-lint"
  golangci-lint run ./...
else
  echo "→ golangci-lint (skipped — not installed; CI runs it)"
fi

if command -v govulncheck >/dev/null 2>&1; then
  echo "→ govulncheck"
  govulncheck ./...
else
  echo "→ govulncheck (skipped — not installed; CI runs it)"
fi

echo "→ build binary for live checks"
go build -o bin/pare ./cmd/pare
BIN="$(pwd)/bin/pare"

echo "→ smoke: version / passthrough / buried-error survives a small budget"
"$BIN" version
printf 'a\nb\nc\n' | "$BIN" | grep -qx b
{ seq 1 200; echo "ERROR: boom"; seq 201 400; } | "$BIN" --budget-bytes 300 --head 3 --tail 3 | grep -q 'ERROR: boom'
echo "✓ all checks passed"

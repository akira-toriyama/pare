# CLAUDE.md — pare

pare is a small, composable **pipe filter** (Go): it reads stdin and writes a
byte-budget-bounded truncation to stdout, keeping head + tail + error lines with
context. See [README.md](README.md) for behavior and [docs/algorithm.md](docs/algorithm.md)
for the budget policy.

## Layout (dependency rule: cmd → cli → budget/version; budget imports nothing local)

- `cmd/pare/main.go` — thin entry; just `os.Exit(cli.Execute())`.
- `internal/budget` — the **pure** core (no I/O, no globals). All truncation
  logic lives here and is unit-tested directly. Do not add I/O to this package.
- `internal/cli` — cobra adapter: flags, stdin/stdout, exit-code contract.
- `internal/version` — build identity (ldflags-injected, VCS fallback).

## Conventions

- **Verify with `sh scripts/check.sh`** (build / vet / `test -race` / lint /
  smoke). Green here ⇒ green CI.
- **Exit codes:** `0` ok · `2` usage/validation · `3` internal/IO. Errors go to
  **stderr** only; stdout stays pure data.
- **Commits:** gitmoji + Conventional Commits
  ([CONTRIBUTING](https://github.com/akira-toriyama/.github/blob/main/CONTRIBUTING.md)).
  Enable the hook: `git config core.hooksPath scripts/hooks`.
- **Docs:** keep README.md and README.ja.md in sync on any user-visible change
  (same facts). Keep both **version-agnostic** — never hardcode a release number
  (link to Releases instead).
- **Third-party GitHub Actions are pinned to a commit SHA** with a `# vX` comment
  (Dependabot bumps them).

## Releasing

Tag `vX.Y.Z` and push → `.github/workflows/release.yml` runs git-cliff +
GoReleaser (binaries, checksums, Homebrew cask, build-provenance attestation).
The Homebrew cask push needs the `HOMEBREW_TAP_TOKEN` secret; without it the
release still succeeds and skips only the cask.

## Task tracking

Work is tracked in the central `projects` furrow board, scoped to this repo.
From inside this checkout: `furrow next` / `furrow ls`. PRs may carry a
`SetStatus-task:` footer to move a task's status on open/merge.

## Fleet-managed files (do not hand-edit here)

`.github/workflows/{task-status,commit-lint,taplo,zizmor}.yml`,
`.github/{dependabot,zizmor}.yml`, and `docs/commit-convention.md` are distributed
by the org `.github` repo's fleet-sync and overwritten on its next run — edit the
canonical copies there. (`build.yml`, `release.yml`, and `govulncheck.yml` carry
no fleet header and are the genuinely repo-local, editable workflows.)

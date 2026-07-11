# pare

[日本語](README.ja.md)

**Context-budget-aware output truncation for AI coding agents.** pare reads
stdin and writes a trimmed version to stdout that fits within a byte budget,
keeping the first lines (**head**), the last lines (**tail**), and any **error
lines** with surrounding context — the middle that a blind `| tail` throws away.

```
your-command 2>&1 | pare
```

## Why

Agents (and humans) defensively pipe noisy commands through `| tail` to avoid
flooding a context window. But a blind tail drops errors that occur in the
*middle* of the output, so the one line you needed is gone and the command gets
re-run. pare keeps the head, the tail, **and** the error lines within a fixed
byte budget, so the failure is visible in a single pass.

```
noise 1
noise 2
noise 3
[... 395 lines omitted ...]
noise 399
noise 400
ERROR: undefined symbol _foo at link time      ← a blind `| tail` drops this
noise 401
noise 402
[... 395 lines omitted ...]
noise 798
noise 799
noise 800
```

## Install

```sh
# Homebrew (macOS/Linux)
brew install akira-toriyama/tap/pare

# Go
go install github.com/akira-toriyama/pare/cmd/pare@latest

# Nix (source-built; reports version "dev")
nix run github:akira-toriyama/pare
```

Prebuilt binaries and checksums for every release are on the
[Releases](https://github.com/akira-toriyama/pare/releases) page.

## Usage

```sh
# defaults: 8 KiB budget, 15 head, 15 tail, 2 context, built-in error pattern
some-build 2>&1 | pare

# tighter budget, capture the full log, add an extra matcher
make 2>&1 | pare --budget-bytes 4096 --tee /tmp/build.log --match WARN

# keep the upstream exit code visible to the shell
set -o pipefail; go test ./... 2>&1 | pare

# test runs: keep the whole failing assertion block, collapse the passes
go test -v ./... 2>&1 | pare --profile test
swift test 2>&1 | pare --profile test
```

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--budget-bytes` | `8192` | Byte ceiling for the output. |
| `--head` | `15` | Lines always kept from the top. |
| `--tail` | `15` | Lines always kept from the bottom. |
| `--context` | `2` | Lines of context kept around each matched line. |
| `--match` | built-in | Error-line regex ([RE2](https://github.com/google/re2/wiki/Syntax)). Repeatable; **replaces** the default when given. |
| `--profile` | – | Extraction profile. `test` tunes matching for test-runner failures and keeps the whole indented assertion block. Empty = generic. |
| `--tee FILE` | – | Write the full, untruncated input to `FILE` and name it in omission markers. |

The built-in matcher is, case-insensitively:

```
\b(error|fail(ed|ure)?|exception|fatal|panic|abort|denied|traceback|undefined symbol|cannot find|assert)\b
```

Pass one or more `--match` to override it (e.g. `--match 'WARN|deprecated'`).

### The `test` profile

`--profile test` is for piping a test runner's output. It changes two things:

- **Structural failure anchors** instead of the generic error-word regex — it
  keys off `--- FAIL:` / `FAIL` / `panic:` (Go), `: error:` and `✘` (Swift
  XCTest / Swift Testing), `●` `✕` `×` (jest / vitest), `FAILED` / `E ` (pytest),
  and `file:line:col:` build diagnostics — so ordinary log prose and passing
  lines don't match.
- **Whole assertion block** instead of a fixed `--context` radius — when an
  anchor matches, pare keeps the contiguous indented body around it (the
  `Error Trace` / `expected` / `actual` / got-want detail a runner prints),
  whether it sits below the header (`go test`) or above it (`go test -v`). The
  passing tests still collapse into `[... N lines omitted ...]`.

Everything else is unchanged: pare still only **selects** verbatim lines (it
never summarizes, counts, or emits JSON), still honors `--budget-bytes` /
`--tee`, and still never reads the upstream exit code. An explicit `--match`
still wins as the matcher; the profile's block behavior applies either way.

```sh
go test -v ./... 2>&1 | pare --profile test
swift test  2>&1 | pare --profile test
pnpm test   2>&1 | pare --profile test
```

### Two things to know about pipes

- **Merge stderr in.** Most errors go to *stderr*, so pipe it into pare with
  `2>&1 |`, otherwise pare only sees stdout.
- **pare never sees the upstream exit code.** It is a filter, so its own exit
  status is about *pare*, not the command feeding it. When the upstream result
  matters, use `set -o pipefail` so the shell still fails on the producer's
  non-zero exit.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | OK — pare ran (this says nothing about the upstream command; see above). |
| `2` | Usage / validation error (bad flag, invalid `--match` regex). |
| `3` | Internal / I/O error (could not read stdin or write the `--tee` file). |

Errors are printed to **stderr**, so a downstream `| jq` or `| grep` on stdout
is never polluted.

## How it works

pare reserves head and tail, adds error blocks oldest-first, and — when over
budget — shrinks context, then drops error blocks from the back, then shrinks
head/tail toward a floor. The full policy is in
[docs/algorithm.md](docs/algorithm.md). Deliberate limits are in
[docs/non-goals.md](docs/non-goals.md).

## Development

```sh
sh scripts/check.sh        # build / vet / test -race / lint / smoke
git config core.hooksPath scripts/hooks   # enable the commit-msg convention hook
```

Commits follow [gitmoji + Conventional Commits](https://github.com/akira-toriyama/.github/blob/main/CONTRIBUTING.md).

## License

[MIT](LICENSE)

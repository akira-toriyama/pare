# Non-goals

What pare deliberately does **not** do, so the tool stays small and composable.

## v1 non-goals

- **Deduplication / summarization.** pare selects lines; it never rewrites,
  collapses repeats, or summarizes. Reach for a dedicated tool (e.g. rtk) when
  you want that.
- **Per-tool format profiles.** pare treats all input as plain lines with a
  regex notion of "error". Structured per-tool parsing (Go test, cargo, tsc, …
  via reviewdog/errorformat) is planned for a future major version, not v1.
- **Streaming / follow.** pare reads stdin to EOF, then writes. It is a filter
  for bounded command output, not a `tail -f` replacement.
- **Reading the upstream exit code.** pare is a pipe filter and cannot see the
  producer's exit status. Use `set -o pipefail` (and `2>&1` to include stderr)
  when the upstream result matters. This is documented, not a bug.

## Deferred (candidate for a later version)

- **A `pare run -- <cmd>` subcommand** that runs a command, truncates its output,
  and passes the command's own exit code through (removing the pipefail caveat).
- **A `PostToolUse` hook** for automatic application to every agent Bash call,
  once the manual-pipe workflow has proven the defaults.
- **A `nix` version stamped with the release number** (today the flake reports
  `dev` for source builds by design — see flake.nix).
- **A man page.** cobra can generate one, but only by pulling a markdown→roff
  dependency (`go-md2man` and its transitive tree) into a single-command filter
  — a poor supply-chain trade. `pare --help` (with cobra `Example:` blocks) and
  the built-in `pare completion <shell>` cover the ergonomics a man page would.

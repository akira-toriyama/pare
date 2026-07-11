# The budget algorithm

`internal/budget` decides which lines of the input survive so the output fits a
byte budget while keeping what matters for debugging. It is a pure function —
deterministic, no I/O — so it is unit-tested directly.

## Inputs

- `BudgetBytes` — the byte ceiling for the output.
- `Head` / `Tail` — lines always kept from the top / bottom.
- `Context` — lines kept on each side of a matched region.
- `Matchers` — regexes that mark a line as "error-ish" (OR-ed together).
- `Extent` — how a match expands into a must-keep region before `Context` is
  added. `ExtentLine` (default) keeps just the matched line; `ExtentBlock` keeps
  the contiguous, strictly-more-indented lines above and below it — the indented
  assertion body a test runner prints under (or, with `go test -v`, above) a
  failure header. `ExtentBlock` backs the `test` profile.
- `TeePath` — when set, named inside omission markers.

## Policy

1. **Fast path.** If the input already fits the budget (or no budget is set),
   return it unchanged. pare is never worse than the raw output for small
   results — a `strings.Split`/`Join` round-trip is byte-identical.
2. **Reserve head and tail.** The first `Head` and last `Tail` lines are the
   base set that is kept before anything else.
3. **Add error blocks, oldest first.** Each match becomes a core region — a
   single line (`ExtentLine`) or its whole indented assertion block
   (`ExtentBlock`) — which then expands by `Context` lines on each side;
   overlapping/adjacent blocks merge. Blocks are added from the top of the file
   downward.
4. **When over budget, shrink in this order:**
   1. **Context** — reduce the context radius from `Context` down to `0`.
   2. **Drop error blocks from the back** — discard the newest (highest-line)
      blocks first, keeping the oldest.
   3. **Shrink head/tail toward the floor** — as a last resort, reduce head and
      tail down to a floor of 3 lines each (never below what the caller asked
      for). At the floor the output may slightly exceed the budget rather than
      collapse to nothing.

Every gap between kept regions becomes exactly one omission marker line:

```
[... 42 lines omitted ...]
[... 42 lines omitted (full: /tmp/build.log) ...]   # when --tee is set
```

The marker counts real omitted lines and, with `--tee`, points at the full
capture.

## Why head/tail are shrunk last

With a sane budget (the default is 8 KiB) and modest head/tail (15 each), the
head and tail never consume the whole budget, so error blocks fit in between and
are shown — which is the entire point of the tool. Head/tail shrinking only
triggers under a pathologically small budget, where keeping the very first and
last lines is the most useful fallback.

## Profiles

A profile preselects the matcher and the `Extent`. The only one today is
`test` (`TestPattern` + `ExtentBlock`), which keeps the whole failing assertion
block a test runner prints. A profile only ever changes *which lines are
selected* — the budget policy above is identical. See the README for the anchor
set it recognizes.

## Non-goals

Deduplication, summarization, structured (JSON) per-tool parsing, and
streaming/follow are out of scope — see [non-goals.md](non-goals.md).

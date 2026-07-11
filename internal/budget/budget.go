// Package budget implements pare's core: fitting arbitrary command output into
// a byte budget while preserving the lines most useful for debugging — the
// head, the tail, and any lines matching "error" patterns (with surrounding
// context). Everything a blind `| tail` throws away in the middle.
//
// It is a pure package: no I/O, no globals, fully deterministic. The CLI reads
// stdin, tees the full output when asked, then hands the bytes to Pare. See
// docs/algorithm.md for the budget policy this implements.
package budget

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// DefaultPattern matches the error-ish lines pare protects when no --match is
// given. Word-anchored and case-insensitive so it fires on real diagnostics
// without swallowing ordinary prose. RE2 (Go regexp) supports \b and (?i).
const DefaultPattern = `(?i)\b(error|fail(ed|ure)?|exception|fatal|panic|abort|denied|traceback|undefined symbol|cannot find|assert)\b`

// TestPattern matches the structural failure anchors of common test runners. It
// backs the `test` profile (paired with ExtentBlock). Unlike DefaultPattern it
// keys off line STRUCTURE — a FAIL header, a failure mark, a file:line:col
// diagnostic — rather than the word "error" in prose, so passing lines and
// ordinary logs do not match. Matched against individual lines (^ is line
// start). Covers, in order: Go `--- FAIL:` (incl. indented subtests), Go
// package `FAIL` summary and bare `FAIL`, panics, Swift-Testing/jest/vitest fail
// marks (✘✗●✕×), XCTest and clang/gcc `: error:` lines, pytest `FAILED` summary
// and `E ` detail, and file:line:col build diagnostics (Go and others).
const TestPattern = `^\s*--- FAIL:` + // Go (sub)test failure header
	`|^FAIL\b` + //             Go package summary + bare FAIL
	`|^panic:` + //             Go / runtime panic
	`|^\s*[✘✗●✕×]` + //         Swift Testing / jest / vitest fail marks
	`|: error:` + //            XCTest, clang/gcc: "file:line: error:"
	`|^FAILED\b` + //           pytest short-summary line
	`|^E {2,}` + //             pytest error-detail lines ("E   assert ...")
	`|\.\w+:\d+:\d+:` //        Go build / file:line:col diagnostics

// Extent selects how a matched line expands into a must-keep region before the
// budget machinery adds Context around it.
type Extent int

const (
	// ExtentLine keeps just the matched line (pare's default behavior).
	ExtentLine Extent = iota
	// ExtentBlock also keeps the contiguous, strictly-more-indented (non-blank)
	// lines immediately above and below the match — the assertion body a test
	// runner prints under (or, with `go test -v`, above) a failure header — as
	// one unit. It backs the `test` profile. It still only ever selects verbatim
	// input lines, so pare's byte-identical-subset contract holds.
	ExtentBlock
)

// floorLines is the minimum head/tail auto-shrink will leave. Below a
// pathologically small budget pare keeps at least this many head and tail
// lines (unless the caller asked for fewer) rather than collapsing to nothing.
const floorLines = 3

// Options configures a Pare call. The CLI always supplies these; the zero value
// (BudgetBytes 0) is treated as "no budget" and returns the input unchanged.
type Options struct {
	BudgetBytes int              // total byte ceiling for the output
	Head        int              // lines kept from the top
	Tail        int              // lines kept from the bottom
	Context     int              // lines of context kept around each matched line
	Matchers    []*regexp.Regexp // error-line matchers (OR-ed); nil ⇒ head/tail only
	Extent      Extent           // how a match expands into a must-keep region
	TeePath     string           // when set, referenced inside omission markers
}

// Result reports what Pare produced. Output is the truncated (or, when it
// already fit, untouched) text. The counts are of real input lines, excluding
// the omission-marker lines Pare inserts.
type Result struct {
	Output       []byte
	Truncated    bool
	InputLines   int
	KeptLines    int
	OmittedLines int
}

// span is a half-open range [start,end) of line indices.
type span struct{ start, end int }

// Pare fits input within opts.BudgetBytes. If the input already fits (or no
// budget is set) it is returned unchanged — pare is never worse than the raw
// output for small results. Otherwise it keeps head, tail, and matched error
// regions with context, filling omission markers into the gaps, following this
// policy when over budget: reserve head/tail, add error blocks oldest-first,
// then on overflow shrink context → drop error blocks from the back → shrink
// head/tail down to the floor.
func Pare(input []byte, opts Options) Result {
	inputLines := countLines(input)
	if opts.BudgetBytes <= 0 || len(input) <= opts.BudgetBytes {
		return Result{Output: input, InputLines: inputLines, KeptLines: inputLines}
	}

	lines, trailingNL := splitLines(input)
	n := len(lines)

	var matchIdx []int
	for i, ln := range lines {
		for _, re := range opts.Matchers {
			if re != nil && re.MatchString(ln) {
				matchIdx = append(matchIdx, i)
				break
			}
		}
	}
	cores := coreSpans(lines, matchIdx, opts.Extent)

	h0 := clamp(opts.Head, 0, n)
	t0 := clamp(opts.Tail, 0, n)
	floorH := min(h0, floorLines)
	floorT := min(t0, floorLines)
	maxCtx := max(opts.Context, 0)
	if len(cores) == 0 {
		maxCtx = 0 // nothing to contextualize; skip the redundant sweep
	}

	h, t := h0, t0
	for {
		base := baseSpans(h, t, n)

		// Phase A: keep every error block, shrinking context from max to 0.
		for c := maxCtx; c >= 0; c-- {
			if out, omitted, ok := tryPlan(lines, combine(base, expandBlocks(cores, c, n)), trailingNL, opts); ok {
				return truncated(out, inputLines, n, omitted)
			}
		}

		// Phase B: context 0, discard error blocks from the back (newest first).
		// k == 0 tests head/tail alone.
		blocks0 := expandBlocks(cores, 0, n)
		for k := len(blocks0); k >= 0; k-- {
			if out, omitted, ok := tryPlan(lines, combine(base, blocks0[:k]), trailingNL, opts); ok {
				return truncated(out, inputLines, n, omitted)
			}
		}

		// Nothing fit at this head/tail. Shrink toward the floor, or accept the
		// floor (head/tail only, possibly over budget) once we reach it.
		if h <= floorH && t <= floorT {
			out, omitted := renderPlan(lines, base, trailingNL, opts.TeePath)
			return truncated(out, inputLines, n, omitted)
		}
		if h-floorH >= t-floorT && h > floorH {
			h--
		} else if t > floorT {
			t--
		} else {
			h-- // unreachable given the guard above, but keeps progress total
		}
	}
}

// tryPlan renders plan and reports whether it fits the byte budget.
func tryPlan(lines []string, plan []span, trailingNL bool, opts Options) (out []byte, omitted int, ok bool) {
	out, omitted = renderPlan(lines, plan, trailingNL, opts.TeePath)
	return out, omitted, len(out) <= opts.BudgetBytes
}

// truncated builds a Result for a paring that went through the budget machinery.
// Truncated is gated on whether any line was actually dropped: every plan that
// tryPlan accepts has omitted > 0 (a zero-omission plan reconstructs the whole
// input, whose length exceeds the budget, so it fails the fit check), leaving
// only the floor return — where head+tail already spans the input — able to
// carry omitted == 0. Reporting Truncated: false there keeps the Result honest:
// byte-identical, unchanged output never claims a truncation happened.
func truncated(out []byte, inputLines, n, omitted int) Result {
	return Result{Output: out, Truncated: omitted > 0, InputLines: inputLines, KeptLines: n - omitted, OmittedLines: omitted}
}

// renderPlan emits the kept spans in order, inserting one omission marker for
// each gap (including a trailing gap). It returns the bytes and the number of
// real lines omitted.
func renderPlan(lines []string, plan []span, trailingNL bool, teePath string) (out []byte, omitted int) {
	n := len(lines)
	var b strings.Builder
	first := true
	write := func(s string) {
		if !first {
			b.WriteByte('\n')
		}
		b.WriteString(s)
		first = false
	}

	prev := 0
	for _, sp := range plan {
		if sp.start > prev {
			k := sp.start - prev
			omitted += k
			write(marker(k, teePath))
		}
		for i := sp.start; i < sp.end; i++ {
			write(lines[i])
		}
		prev = sp.end
	}
	if prev < n {
		k := n - prev
		omitted += k
		write(marker(k, teePath))
	}

	s := b.String()
	if trailingNL {
		s += "\n"
	}
	return []byte(s), omitted
}

// marker is the single omission-marker line. When a tee path is set it points
// the reader at the full, untruncated capture.
func marker(k int, teePath string) string {
	unit := "lines"
	if k == 1 {
		unit = "line"
	}
	if teePath != "" {
		return fmt.Sprintf("[... %d %s omitted (full: %s) ...]", k, unit, teePath)
	}
	return fmt.Sprintf("[... %d %s omitted ...]", k, unit)
}

// baseSpans is the always-kept head/tail region for a given head/tail count.
func baseSpans(h, t, n int) []span {
	var spans []span
	if h > 0 {
		spans = append(spans, span{0, min(h, n)})
	}
	if t > 0 {
		spans = append(spans, span{max(n-t, 0), n})
	}
	return mergeSpans(spans)
}

// coreSpans turns matched line indices into the minimal must-keep span for each
// match, before context is added. ExtentLine yields the single matched line;
// ExtentBlock yields the whole indented assertion block the match heads.
func coreSpans(lines []string, idx []int, ext Extent) []span {
	if len(idx) == 0 {
		return nil
	}
	out := make([]span, 0, len(idx))
	for _, i := range idx {
		if ext == ExtentBlock {
			out = append(out, blockExtent(lines, i))
		} else {
			out = append(out, span{i, i + 1})
		}
	}
	return out
}

// blockExtent expands a matched anchor line into the assertion block it heads:
// the anchor plus the contiguous run of strictly-more-indented, non-blank lines
// immediately above and below it. That captures the indented body a runner
// prints for a failure — the file:line and got/want detail — whether it sits
// below the FAIL header (go test) or above it (go test -v), without a
// per-framework parser.
func blockExtent(lines []string, i int) span {
	base := indentWidth(lines[i])
	start, end := i, i+1
	for j := i - 1; j >= 0; j-- {
		if isBlank(lines[j]) || indentWidth(lines[j]) <= base {
			break
		}
		start = j
	}
	for j := i + 1; j < len(lines); j++ {
		if isBlank(lines[j]) || indentWidth(lines[j]) <= base {
			break
		}
		end = j + 1
	}
	return span{start, end}
}

// indentWidth counts leading spaces and tabs (each as one unit — enough for the
// relative comparison blockExtent needs).
func indentWidth(s string) int {
	w := 0
	for _, r := range s {
		if r != ' ' && r != '\t' {
			break
		}
		w++
	}
	return w
}

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

// expandBlocks pads each core span by context lines on both sides and merges
// overlaps. The result is sorted by start, so keeping a prefix drops the latest
// blocks.
func expandBlocks(cores []span, context, n int) []span {
	if len(cores) == 0 {
		return nil
	}
	spans := make([]span, 0, len(cores))
	for _, c := range cores {
		spans = append(spans, span{max(c.start-context, 0), min(c.end+context, n)})
	}
	return mergeSpans(spans)
}

// combine concatenates span groups and merges them into a normalized set.
func combine(groups ...[]span) []span {
	var all []span
	for _, g := range groups {
		all = append(all, g...)
	}
	return mergeSpans(all)
}

// mergeSpans returns a sorted, overlap- and adjacency-merged copy of spans, so
// two touching spans never leave a zero-line gap that would emit a marker.
func mergeSpans(spans []span) []span {
	if len(spans) == 0 {
		return nil
	}
	s := append([]span(nil), spans...)
	sort.Slice(s, func(i, j int) bool { return s[i].start < s[j].start })
	out := []span{s[0]}
	for _, sp := range s[1:] {
		last := &out[len(out)-1]
		if sp.start <= last.end {
			if sp.end > last.end {
				last.end = sp.end
			}
			continue
		}
		out = append(out, sp)
	}
	return out
}

// splitLines splits input into lines, reporting whether it ended with a
// newline. strings.Split/Join round-trips exactly, so an untruncated rebuild is
// byte-identical to the input.
func splitLines(input []byte) (lines []string, trailingNL bool) {
	s := string(input)
	if s == "" {
		return nil, false
	}
	trailingNL = strings.HasSuffix(s, "\n")
	if trailingNL {
		s = s[:len(s)-1]
	}
	return strings.Split(s, "\n"), trailingNL
}

// countLines counts real lines (a trailing newline does not add one), matching
// len(splitLines(input)) without allocating the slice.
func countLines(input []byte) int {
	if len(input) == 0 {
		return 0
	}
	s := string(input)
	c := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		c++
	}
	return c
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

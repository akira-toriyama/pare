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
	"bytes"
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// DefaultPattern matches the error-ish lines pare protects when no --match is
// given. Word-anchored and case-insensitive so it fires on real diagnostics
// without swallowing ordinary prose. RE2 (Go regexp) supports \b and (?i).
const DefaultPattern = `(?i)\b(error|fail(ed|ure)?|exception|fatal|panic|abort|denied|traceback|undefined symbol|cannot find|assert)\b`

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

// Options configures a Pare call. Defaults is the starting point the CLI seeds
// its flags from; the zero value (BudgetBytes 0) is treated as "no budget" and
// returns the input unchanged.
type Options struct {
	BudgetBytes int              // total byte ceiling for the output
	Head        int              // lines kept from the top
	Tail        int              // lines kept from the bottom
	Context     int              // lines of context kept around each matched line
	Matchers    []*regexp.Regexp // error-line matchers (OR-ed); nil ⇒ head/tail only
	Extent      Extent           // how a match expands into a must-keep region
	TeePath     string           // when set, referenced inside omission markers
}

// Defaults are the option values pare uses when a flag is not given — the
// single source the CLI seeds its flag defaults from, so `--help` reports the
// real numbers. Matchers is nil here: the pattern comes from the Profile in
// force once the caller has resolved --match.
func Defaults() Options {
	return Options{BudgetBytes: 8192, Head: 15, Tail: 15, Context: 2, Extent: ExtentLine}
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
//
// Every candidate plan is measured with layout.size (prefix sums, no
// rendering); only the plan finally chosen is rendered.
func Pare(input []byte, opts Options) Result {
	inputLines := countLines(input)
	if opts.BudgetBytes <= 0 || len(input) <= opts.BudgetBytes {
		return Result{Output: input, InputLines: inputLines, KeptLines: inputLines}
	}

	lines, trailingNL := splitLines(input)
	n := len(lines)
	lay := newLayout(lines, trailingNL, opts.TeePath)
	fits := func(plan []span) bool { return lay.size(plan) <= opts.BudgetBytes }

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

		// Phase A: keep every error block, shrinking context from max to 0. Not
		// monotone in c (narrowing a block that touched head/tail opens a gap
		// whose marker can outweigh the lines it replaces), so this is a scan.
		for c := maxCtx; c >= 0; c-- {
			if plan := combine(base, expandBlocks(cores, c, n)); fits(plan) {
				return lay.render(plan, n)
			}
		}

		// Phase B: context 0, discard error blocks from the back (newest first);
		// k == 0 is head/tail alone. Dropping the last block never grows the
		// output unless the plan with it was the whole input (which cannot fit),
		// so fits is monotone in k and the largest fitting k is found by bisection.
		blocks0 := expandBlocks(cores, 0, n)
		fitsK := func(k int) bool { return fits(combine(base, blocks0[:k])) }
		if fitsK(0) {
			lo, hi := 0, len(blocks0)
			for lo < hi {
				mid := (lo + hi + 1) / 2
				if fitsK(mid) {
					lo = mid
				} else {
					hi = mid - 1
				}
			}
			return lay.render(combine(base, blocks0[:lo]), n)
		}

		// Nothing fit at this head/tail. Shrink toward the floor, or accept the
		// floor (head/tail only, possibly over budget) once we reach it.
		if h <= floorH && t <= floorT {
			return lay.render(base, n)
		}
		// Whichever side is further from its floor gives a line; the guard above
		// rules out both being at the floor, so the chosen side is above it.
		if h-floorH >= t-floorT {
			h--
		} else {
			t--
		}
	}
}

// layout is the split input plus what measuring a plan needs: the byte prefix
// sum of the lines and the marker geometry. size and render agree byte for
// byte — that equality is the contract the bisection in Pare rests on, and
// TestLayout_SizeMatchesRender pins it.
type layout struct {
	lines      []string
	pre        []int // pre[i] = total bytes of lines[:i]
	trailingNL bool
	teePath    string
	markerBase int // len(marker(k, teePath)) minus k's digits and the plural s
}

func newLayout(lines []string, trailingNL bool, teePath string) layout {
	pre := make([]int, len(lines)+1)
	for i, ln := range lines {
		pre[i+1] = pre[i] + len(ln)
	}
	return layout{
		lines:      lines,
		pre:        pre,
		trailingNL: trailingNL,
		teePath:    teePath,
		markerBase: len(marker(1, teePath)) - 1,
	}
}

// markerLen is len(marker(k, l.teePath)) without formatting it.
func (l layout) markerLen(k int) int {
	size := l.markerBase + digits(k)
	if k != 1 {
		size++ // "lines"
	}
	return size
}

// size is len(render(plan).Output): kept bytes + one marker per gap, joined by
// single newlines, plus the trailing newline when the input had one.
func (l layout) size(plan []span) int {
	n := len(l.lines)
	total, items, prev := 0, 0, 0
	for _, sp := range plan {
		if sp.start > prev {
			total += l.markerLen(sp.start - prev)
			items++
		}
		total += l.pre[sp.end] - l.pre[sp.start]
		items += sp.end - sp.start
		prev = sp.end
	}
	if prev < n {
		total += l.markerLen(n - prev)
		items++
	}
	if items > 0 {
		total += items - 1
	}
	if l.trailingNL {
		total++
	}
	return total
}

// render emits the kept spans in order, inserting one omission marker for each
// gap (including a trailing gap), and wraps the bytes in a Result. n is the
// input's real line count (here equal to len(l.lines), so it serves as both
// InputLines and the base for KeptLines). Truncated is gated on whether any
// line was actually dropped: every plan Pare accepts by size has omitted > 0 (a
// zero-omission plan reconstructs the whole input, whose length exceeds the
// budget), leaving only the floor return — where head+tail already spans the
// input — able to carry omitted == 0. Reporting Truncated: false there keeps
// the Result honest: byte-identical, unchanged output never claims a
// truncation happened.
func (l layout) render(plan []span, n int) Result {
	var b strings.Builder
	b.Grow(l.size(plan))
	first := true
	write := func(s string) {
		if !first {
			b.WriteByte('\n')
		}
		b.WriteString(s)
		first = false
	}

	omitted, prev := 0, 0
	for _, sp := range plan {
		if sp.start > prev {
			k := sp.start - prev
			omitted += k
			write(marker(k, l.teePath))
		}
		for i := sp.start; i < sp.end; i++ {
			write(l.lines[i])
		}
		prev = sp.end
	}
	if prev < n {
		k := n - prev
		omitted += k
		write(marker(k, l.teePath))
	}
	if l.trailingNL {
		b.WriteByte('\n')
	}
	return Result{Output: []byte(b.String()), Truncated: omitted > 0, InputLines: n, KeptLines: n - omitted, OmittedLines: omitted}
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

// digits is the decimal width of k >= 0.
func digits(k int) int {
	d := 1
	for k >= 10 {
		k /= 10
		d++
	}
	return d
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

func combine(groups ...[]span) []span {
	return mergeSpans(slices.Concat(groups...))
}

// mergeSpans returns a sorted, overlap- and adjacency-merged copy of spans, so
// two touching spans never leave a zero-line gap that would emit a marker.
func mergeSpans(spans []span) []span {
	if len(spans) == 0 {
		return nil
	}
	s := slices.Clone(spans)
	slices.SortFunc(s, func(a, b span) int { return cmp.Compare(a.start, b.start) })
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
// len(splitLines(input)) without allocating the slice or copying the input.
func countLines(input []byte) int {
	if len(input) == 0 {
		return 0
	}
	c := bytes.Count(input, []byte{'\n'})
	if input[len(input)-1] != '\n' {
		c++
	}
	return c
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

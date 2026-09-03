package budget

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// markerRe matches an omission-marker line (with or without a tee path).
var markerRe = regexp.MustCompile(`^\[\.\.\. \d+ lines? omitted( \(full: .*\))? \.\.\.\]$`)

// assertInvariants checks pare's core contract for one Pare call: when the
// input already fits the output is byte-identical (never worse), counts stay
// non-negative, and every non-marker output line is a verbatim line from the
// input — pare selects lines, it never fabricates or mutates them. Lines are
// split on "\n" exactly as splitLines does, so an over-long line is no
// special case here.
func assertInvariants(t *testing.T, input []byte, opts Options, res Result) {
	t.Helper()
	if opts.BudgetBytes == 0 || len(input) <= opts.BudgetBytes {
		if !bytes.Equal(res.Output, input) {
			t.Fatalf("fast path must be identity:\n in=%q\nout=%q", input, res.Output)
		}
		return
	}
	if res.OmittedLines < 0 || res.KeptLines < 0 {
		t.Fatalf("negative counts: %+v", res)
	}
	inputLines := make(map[string]bool)
	for _, ln := range strings.Split(string(input), "\n") {
		inputLines[ln] = true
	}
	for _, ln := range strings.Split(string(res.Output), "\n") {
		if markerRe.MatchString(ln) {
			continue
		}
		if !inputLines[ln] {
			t.Fatalf("output line was not present in the input: %q", ln)
		}
	}
}

// fuzzPare is the shared fuzz body: clamp the ints into productive ranges (the
// fuzzer loves MinInt), run Pare with the given matcher and extent, and assert
// the invariants.
func fuzzPare(re *regexp.Regexp, ext Extent) func(t *testing.T, input []byte, budget, head, tail, ctx int) {
	return func(t *testing.T, input []byte, budget, head, tail, ctx int) {
		opts := Options{
			BudgetBytes: clamp(budget, 0, 1<<20),
			Head:        clamp(head, 0, 100000),
			Tail:        clamp(tail, 0, 100000),
			Context:     clamp(ctx, 0, 10000),
			Matchers:    []*regexp.Regexp{re},
			Extent:      ext,
		}
		assertInvariants(t, input, opts, Pare(input, opts))
	}
}

// FuzzPare asserts the core invariants on arbitrary input and options with the
// generic profile (DefaultPattern + ExtentLine).
func FuzzPare(f *testing.F) {
	for _, s := range []string{
		"", "a", "a\n", "a\nb\nc\n", "hello\nERROR: boom\nworld\n",
		strings.Repeat("line\n", 100), "no-trailing-newline",
	} {
		f.Add([]byte(s), 100, 3, 3, 1)
	}
	f.Fuzz(fuzzPare(regexp.MustCompile(DefaultPattern), ExtentLine))
}

// FuzzPareBlockExtent asserts the same invariants for the `test` profile path
// (TestPattern + ExtentBlock): block extent widens what is kept but must still
// only ever select verbatim input lines and never panic or exceed the fast-path
// identity guarantee. CI fuzzes it separately — `go test -fuzz` accepts a
// single target per run.
func FuzzPareBlockExtent(f *testing.F) {
	for _, s := range []string{
		"", "a\n", "--- FAIL: TestX (0.0s)\n    x_test.go:1: got 1 want 2\n",
		"pass\npass\n--- FAIL: TestY\n\tdetail line\nFAIL\tpkg\t0.1s\n",
		strings.Repeat("--- PASS: TestZ (0.0s)\n", 50) + "panic: boom\n\tgoroutine 1\n",
	} {
		f.Add([]byte(s), 100, 3, 3, 2)
	}
	f.Fuzz(fuzzPare(regexp.MustCompile(TestPattern), ExtentBlock))
}

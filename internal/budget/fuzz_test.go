package budget

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// markerRe matches an omission-marker line (with or without a tee path).
var markerRe = regexp.MustCompile(`^\[\.\.\. \d+ lines? omitted( \(full: .*\))? \.\.\.\]$`)

// FuzzPare asserts pare's core invariants on arbitrary input and options:
//   - it never panics,
//   - when the input already fits, the output is byte-identical (never worse),
//   - counts stay non-negative,
//   - every non-marker output line is a verbatim line from the input — pare
//     selects lines, it never fabricates or mutates them.
func FuzzPare(f *testing.F) {
	for _, s := range []string{
		"", "a", "a\n", "a\nb\nc\n", "hello\nERROR: boom\nworld\n",
		strings.Repeat("line\n", 100), "no-trailing-newline",
	} {
		f.Add([]byte(s), 100, 3, 3, 1)
	}
	re := regexp.MustCompile(DefaultPattern)

	f.Fuzz(func(t *testing.T, input []byte, budget, head, tail, ctx int) {
		// Keep the ints in productive ranges (the fuzzer loves MinInt).
		budget = clamp(budget, 0, 1<<20)
		head = clamp(head, 0, 100000)
		tail = clamp(tail, 0, 100000)
		ctx = clamp(ctx, 0, 10000)

		res := Pare(input, Options{
			BudgetBytes: budget, Head: head, Tail: tail, Context: ctx,
			Matchers: []*regexp.Regexp{re},
		})

		if budget == 0 || len(input) <= budget {
			if !bytes.Equal(res.Output, input) {
				t.Fatalf("fast path must be identity:\n in=%q\nout=%q", input, res.Output)
			}
			return
		}

		if res.OmittedLines < 0 || res.KeptLines < 0 {
			t.Fatalf("negative counts: %+v", res)
		}

		inputLines := make(map[string]bool)
		for _, ln := range scanLines(input) {
			inputLines[ln] = true
		}
		for _, ln := range scanLines(res.Output) {
			if markerRe.MatchString(ln) {
				continue
			}
			if !inputLines[ln] {
				t.Fatalf("output line was not present in the input: %q", ln)
			}
		}
	})
}

func scanLines(b []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

package budget

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func defaultMatchers(tb testing.TB) []*regexp.Regexp {
	tb.Helper()
	re, err := regexp.Compile(DefaultPattern)
	if err != nil {
		tb.Fatalf("DefaultPattern does not compile: %v", err)
	}
	return []*regexp.Regexp{re}
}

// numbered builds n lines "lineNNN", each a fixed width for predictable bytes.
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line%03d\n", i)
	}
	return b.String()
}

func TestPare_FitsUnderBudgetReturnedUnchanged(t *testing.T) {
	in := []byte("alpha\nbeta\ngamma\n")
	res := Pare(in, Options{BudgetBytes: 1 << 20, Head: 15, Tail: 15, Context: 2, Matchers: defaultMatchers(t)})
	if res.Truncated {
		t.Fatalf("expected not truncated")
	}
	if string(res.Output) != string(in) {
		t.Fatalf("output changed:\n got %q\nwant %q", res.Output, in)
	}
	if res.InputLines != 3 || res.KeptLines != 3 || res.OmittedLines != 0 {
		t.Fatalf("bad stats: %+v", res)
	}
}

func TestPare_EmptyInput(t *testing.T) {
	res := Pare(nil, Options{BudgetBytes: 10, Head: 2, Tail: 2})
	if res.Truncated || len(res.Output) != 0 {
		t.Fatalf("empty input should pass through: %+v", res)
	}
}

func TestPare_ZeroBudgetIsNoOp(t *testing.T) {
	in := []byte(numbered(500))
	res := Pare(in, Options{BudgetBytes: 0, Head: 2, Tail: 2})
	if res.Truncated || string(res.Output) != string(in) {
		t.Fatalf("zero budget must not truncate")
	}
}

func TestPare_NoTrailingNewlinePreserved(t *testing.T) {
	in := []byte("a\nb\nc") // no trailing newline
	res := Pare(in, Options{BudgetBytes: 1 << 20, Head: 5, Tail: 5})
	if string(res.Output) != "a\nb\nc" {
		t.Fatalf("trailing-newline handling changed content: %q", res.Output)
	}
}

func TestPare_HeadTailWithMiddleOmitted(t *testing.T) {
	in := []byte(numbered(100)) // 100 lines, no error matches
	// Budget large enough for head+tail+marker but not the whole thing.
	res := Pare(in, Options{BudgetBytes: 120, Head: 2, Tail: 2, Matchers: defaultMatchers(t)})
	if !res.Truncated {
		t.Fatalf("expected truncation")
	}
	out := string(res.Output)
	for _, want := range []string{"line001", "line002", "line099", "line100"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "line050") {
		t.Fatalf("middle line unexpectedly kept:\n%s", out)
	}
	if !strings.Contains(out, "omitted") {
		t.Fatalf("expected an omission marker:\n%s", out)
	}
	if len(res.Output) > 120 {
		t.Fatalf("output %d bytes exceeds budget 120", len(res.Output))
	}
}

func TestPare_ErrorLineKeptWithContext(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		if i == 20 {
			b.WriteString("boom: fatal error here\n")
		} else {
			fmt.Fprintf(&b, "noise line %03d\n", i)
		}
	}
	in := []byte(b.String())
	res := Pare(in, Options{BudgetBytes: 200, Head: 2, Tail: 2, Context: 1, Matchers: defaultMatchers(t)})
	out := string(res.Output)
	if !strings.Contains(out, "boom: fatal error here") {
		t.Fatalf("error line dropped:\n%s", out)
	}
	// context 1 ⇒ the immediate neighbors should be present.
	if !strings.Contains(out, "noise line 019") || !strings.Contains(out, "noise line 021") {
		t.Fatalf("context lines dropped:\n%s", out)
	}
	if len(res.Output) > 200 {
		t.Fatalf("over budget: %d", len(res.Output))
	}
}

func TestPare_TeePathAppearsInMarker(t *testing.T) {
	in := []byte(numbered(100))
	res := Pare(in, Options{BudgetBytes: 150, Head: 2, Tail: 2, TeePath: "/tmp/full.log", Matchers: defaultMatchers(t)})
	if !strings.Contains(string(res.Output), "(full: /tmp/full.log)") {
		t.Fatalf("tee path not referenced in marker:\n%s", res.Output)
	}
}

func TestPare_NoTeePathNoFullClause(t *testing.T) {
	in := []byte(numbered(100))
	res := Pare(in, Options{BudgetBytes: 150, Head: 2, Tail: 2, Matchers: defaultMatchers(t)})
	if strings.Contains(string(res.Output), "(full:") {
		t.Fatalf("unexpected full clause without tee:\n%s", res.Output)
	}
}

func TestPare_ContextShrinksBeforeDroppingBlocks(t *testing.T) {
	// One error at line 20 with wide context available, but a budget that only
	// fits the error line with little/no context. The error line must survive.
	var b strings.Builder
	for i := 1; i <= 60; i++ {
		if i == 30 {
			b.WriteString("PANIC: goroutine stack\n")
		} else {
			fmt.Fprintf(&b, "trace %03d\n", i)
		}
	}
	in := []byte(b.String())
	res := Pare(in, Options{BudgetBytes: 130, Head: 2, Tail: 2, Context: 8, Matchers: defaultMatchers(t)})
	out := string(res.Output)
	if !strings.Contains(out, "PANIC: goroutine stack") {
		t.Fatalf("error line must survive context shrink:\n%s", out)
	}
	if len(res.Output) > 130 {
		t.Fatalf("over budget: %d", len(res.Output))
	}
}

func TestPare_DropsErrorBlocksFromTheBack(t *testing.T) {
	// Two well-separated errors. A budget that fits head+tail+one block should
	// keep the FIRST (oldest) error and drop the later one.
	var b strings.Builder
	for i := 1; i <= 80; i++ {
		switch i {
		case 20:
			b.WriteString("error: first failure\n")
		case 60:
			b.WriteString("error: second failure\n")
		default:
			fmt.Fprintf(&b, "log %03d\n", i)
		}
	}
	in := []byte(b.String())
	res := Pare(in, Options{BudgetBytes: 150, Head: 2, Tail: 2, Context: 0, Matchers: defaultMatchers(t)})
	out := string(res.Output)
	if !strings.Contains(out, "error: first failure") {
		t.Fatalf("oldest error should be kept:\n%s", out)
	}
	if strings.Contains(out, "error: second failure") {
		t.Fatalf("newest error should have been dropped from the back:\n%s", out)
	}
	if len(res.Output) > 150 {
		t.Fatalf("over budget: %d", len(res.Output))
	}
}

func TestPare_FloorShrinkUnderTinyBudget(t *testing.T) {
	in := []byte(numbered(200)) // no matches
	// Absurdly small budget: algorithm shrinks head/tail toward the floor.
	res := Pare(in, Options{BudgetBytes: 40, Head: 15, Tail: 15, Matchers: defaultMatchers(t)})
	out := string(res.Output)
	if !strings.Contains(out, "line001") {
		t.Fatalf("should still show the very first line:\n%s", out)
	}
	if !strings.Contains(out, "line200") {
		t.Fatalf("should still show the very last line:\n%s", out)
	}
	// Should have shrunk well below the requested 15 head / 15 tail.
	if strings.Contains(out, "line012") {
		t.Fatalf("head did not shrink toward floor:\n%s", out)
	}
}

func TestPare_FloorPassthroughIsNotReportedTruncated(t *testing.T) {
	// A few but very long lines: head+tail already spans every line, yet the
	// bytes exceed the budget. The shrink loop bottoms out at the floor and
	// returns the whole input verbatim — nothing is omitted, so the Result must
	// NOT claim a truncation (Output byte-identical, OmittedLines 0, Truncated
	// false). Guards the truncated() flag against the self-contradictory state
	// where unchanged output was reported as truncated.
	in := []byte(strings.Repeat(strings.Repeat("x", 100)+"\n", 4)) // 4 lines × 101 bytes
	res := Pare(in, Options{BudgetBytes: 50, Head: 15, Tail: 15, Matchers: defaultMatchers(t)})
	if res.Truncated {
		t.Fatalf("unchanged floor passthrough must not be reported as truncated: %+v", res)
	}
	if string(res.Output) != string(in) {
		t.Fatalf("floor passthrough must return the input verbatim:\n got %q\nwant %q", res.Output, in)
	}
	if res.OmittedLines != 0 || res.KeptLines != res.InputLines {
		t.Fatalf("no line was dropped, counts should reflect that: %+v", res)
	}
}

func TestPare_HeadTailLargerThanInput(t *testing.T) {
	in := []byte("one\ntwo\nthree\n")
	res := Pare(in, Options{BudgetBytes: 1 << 20, Head: 100, Tail: 100})
	if res.Truncated || string(res.Output) != string(in) {
		t.Fatalf("small input with huge head/tail must pass through unchanged: %q", res.Output)
	}
}

func TestPare_OutputNeverExceedsBudgetAboveFloor(t *testing.T) {
	// Property: for a range of budgets comfortably above the floor, the output
	// stays within budget.
	in := []byte(numbered(1000))
	for _, budget := range []int{200, 500, 1000, 2000, 4000} {
		res := Pare(in, Options{BudgetBytes: budget, Head: 10, Tail: 10, Context: 2, Matchers: defaultMatchers(t)})
		if len(res.Output) > budget {
			t.Fatalf("budget %d: output %d bytes exceeds it", budget, len(res.Output))
		}
		if !res.Truncated {
			t.Fatalf("budget %d: expected truncation of a 1000-line input", budget)
		}
	}
}

func TestPare_SingularMarkerGrammar(t *testing.T) {
	// Force exactly one line to be omitted and assert singular grammar. Lines
	// must be long enough that dropping one genuinely saves bytes (a marker is
	// ~24 bytes, so tiny lines would make omission counterproductive).
	long := strings.Repeat("x", 60)
	in := []byte(strings.Repeat(long+"\n", 5)) // 5 lines of 60 chars
	// head 2, tail 2 ⇒ the middle line (index 2) is the single gap. Budget sits
	// between "head+tail+marker" and the full input.
	res := Pare(in, Options{BudgetBytes: 290, Head: 2, Tail: 2})
	if !res.Truncated {
		t.Fatalf("expected truncation")
	}
	if !strings.Contains(string(res.Output), "1 line omitted") {
		t.Fatalf("expected singular grammar:\n%s", res.Output)
	}
}

func TestPare_DefaultPatternMatchesCommonDiagnostics(t *testing.T) {
	re := defaultMatchers(t)[0]
	for _, s := range []string{
		"ERROR: something", "build failed", "Segmentation fault (panic)",
		"fatal: not a git repository", "undefined symbol: _foo", "Traceback (most recent call last):",
		"assertion failed", "permission denied",
	} {
		if !re.MatchString(s) {
			t.Errorf("default pattern should match %q", s)
		}
	}
	for _, s := range []string{"all good", "compiled successfully", "12 passed"} {
		if re.MatchString(s) {
			t.Errorf("default pattern should NOT match %q", s)
		}
	}
}

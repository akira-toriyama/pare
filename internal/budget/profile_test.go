package budget

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func testMatchers(tb testing.TB) []*regexp.Regexp {
	tb.Helper()
	re, err := regexp.Compile(TestPattern)
	if err != nil {
		tb.Fatalf("TestPattern does not compile: %v", err)
	}
	return []*regexp.Regexp{re}
}

// goTest builds go-test output with passesEachSide passing tests on each side of
// a single failing test whose two-line assertion body is indented under the
// `--- FAIL:` header (the non-verbose `go test` layout).
func goTest(passesEachSide int) string {
	var b strings.Builder
	emit := func(from, to int) {
		for i := from; i <= to; i++ {
			fmt.Fprintf(&b, "=== RUN   TestPass%03d\n--- PASS: TestPass%03d (0.00s)\n", i, i)
		}
	}
	emit(1, passesEachSide)
	b.WriteString("--- FAIL: TestBeta (0.00s)\n")
	b.WriteString("    beta_test.go:42: got 3, want 4\n")
	b.WriteString("        values differ at index 0\n")
	emit(passesEachSide+1, passesEachSide*2)
	b.WriteString("FAIL\texample/pkg\t0.123s\n")
	return b.String()
}

func TestTestPattern_MatchesFailureAnchorsNotPasses(t *testing.T) {
	re := testMatchers(t)[0]
	shouldMatch := []string{
		"--- FAIL: TestFoo (0.00s)",                    // Go test failure header
		"    --- FAIL: TestFoo/sub (0.00s)",            // Go indented subtest
		"FAIL\texample/pkg\t0.123s",                    // Go package summary
		"FAIL",                                         // Go bare summary
		"panic: runtime error: index out of range [3]", // Go panic
		"/p/FooTests.swift:12: error: -[FooTests testBar] : XCTAssertEqual failed", // XCTest
		`✘ Test "bar" recorded an issue at File.swift:12:5: Expectation failed`,    // Swift Testing
		"  ● Suite › renders",                       // jest failure header
		"   ✕ adds numbers (3 ms)",                  // jest fail mark
		" × math > adds (2ms)",                      // vitest fail mark
		"FAILED tests/test_foo.py::test_bar - boom", // pytest summary
		"E       assert 1 == 2",                     // pytest error detail
		"./calc.go:5:2: undefined: foo",             // Go build diagnostic
	}
	for _, s := range shouldMatch {
		if !re.MatchString(s) {
			t.Errorf("TestPattern should match %q", s)
		}
	}
	shouldNotMatch := []string{
		"--- PASS: TestFoo (0.00s)",
		"ok  \texample/pkg\t0.456s",
		"PASS",
		"=== RUN   TestFoo",
		"Test Case '-[FooTests testBar]' passed (0.001 seconds).",
		"12 passed, 0 failed",
		"2024-01-02 15:04:05 info: starting server", // timestamp, not a diagnostic
		"E = mc squared is not an error line",       // 'E' + single space only
	}
	for _, s := range shouldNotMatch {
		if re.MatchString(s) {
			t.Errorf("TestPattern should NOT match %q", s)
		}
	}
}

func TestPare_BlockExtentKeepsIndentedBodyBelowAnchor(t *testing.T) {
	in := []byte(goTest(40))
	res := Pare(in, Options{
		BudgetBytes: 500, Head: 3, Tail: 3, Context: 0,
		Matchers: testMatchers(t), Extent: ExtentBlock,
	})
	out := string(res.Output)
	for _, want := range []string{
		"--- FAIL: TestBeta (0.00s)",
		"beta_test.go:42: got 3, want 4",
		"values differ at index 0", // the deepest indented line — only block extent keeps it
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("block body line dropped: %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "TestPass020") {
		t.Fatalf("a passing test in the middle should have been omitted:\n%s", out)
	}
	if len(res.Output) > 500 {
		t.Fatalf("over budget: %d", len(res.Output))
	}
}

// Control: ExtentLine (the default) with context 0 keeps ONLY the matched anchor
// line, dropping the indented body — this is exactly the gap ExtentBlock fills.
// If this ever passed alongside the block test, block extent would be a no-op.
func TestPare_ExtentLineDropsIndentedBody(t *testing.T) {
	in := []byte(goTest(40))
	res := Pare(in, Options{
		BudgetBytes: 500, Head: 3, Tail: 3, Context: 0,
		Matchers: testMatchers(t), Extent: ExtentLine,
	})
	out := string(res.Output)
	if !strings.Contains(out, "--- FAIL: TestBeta (0.00s)") {
		t.Fatalf("anchor line should still be kept:\n%s", out)
	}
	if strings.Contains(out, "values differ at index 0") {
		t.Fatalf("ExtentLine+context0 must NOT keep the indented body (that is block extent's job):\n%s", out)
	}
}

func TestPare_BlockExtentKeepsIndentedBodyAboveAnchor(t *testing.T) {
	// go test -v interleaving: the assertion detail is printed BEFORE the
	// `--- FAIL:` header, so block extent must reach upward too.
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "=== RUN   TestPass%03d\n--- PASS: TestPass%03d (0.00s)\n", i, i)
	}
	b.WriteString("=== RUN   TestBeta\n")
	b.WriteString("    beta_test.go:42: got 3, want 4\n") // detail ABOVE the header
	b.WriteString("--- FAIL: TestBeta (0.00s)\n")
	for i := 41; i <= 80; i++ {
		fmt.Fprintf(&b, "=== RUN   TestPass%03d\n--- PASS: TestPass%03d (0.00s)\n", i, i)
	}
	b.WriteString("FAIL\texample/pkg\t0.123s\n")

	res := Pare([]byte(b.String()), Options{
		BudgetBytes: 500, Head: 3, Tail: 3, Context: 0,
		Matchers: testMatchers(t), Extent: ExtentBlock,
	})
	out := string(res.Output)
	if !strings.Contains(out, "beta_test.go:42: got 3, want 4") {
		t.Fatalf("detail above the anchor should be kept by upward block extent:\n%s", out)
	}
}

func TestPare_BlockExtentIsVerbatimSubset(t *testing.T) {
	in := []byte(goTest(50))
	res := Pare(in, Options{
		BudgetBytes: 400, Head: 4, Tail: 4, Context: 1,
		Matchers: testMatchers(t), Extent: ExtentBlock,
	})
	markerRe := regexp.MustCompile(`^\[\.\.\. \d+ lines? omitted( \(full: .*\))? \.\.\.\]$`)
	inLines := map[string]bool{}
	for _, ln := range strings.Split(string(in), "\n") {
		inLines[ln] = true
	}
	for _, ln := range strings.Split(string(res.Output), "\n") {
		if markerRe.MatchString(ln) {
			continue
		}
		if !inLines[ln] {
			t.Fatalf("block extent fabricated a line not in the input: %q", ln)
		}
	}
}

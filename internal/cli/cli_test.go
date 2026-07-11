package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run builds a fresh root command (flag state is local, so tests don't share
// globals), feeds stdin, and captures stdout+stderr.
func run(args []string, stdin string) (out string, err error) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err = root.Execute()
	return buf.String(), err
}

func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line%04d\n", i)
	}
	return b.String()
}

func TestFilter_PassthroughSmallInput(t *testing.T) {
	in := "alpha\nbeta\ngamma\n"
	out, err := run(nil, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != in {
		t.Fatalf("small input should pass through unchanged:\n got %q\nwant %q", out, in)
	}
}

func TestFilter_TruncatesLargeInput(t *testing.T) {
	in := numbered(1000)
	out, err := run([]string{"--budget-bytes", "400", "--head", "3", "--tail", "3"}, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) > 400 {
		t.Fatalf("output %d bytes exceeds budget 400", len(out))
	}
	for _, want := range []string{"line0001", "line1000", "omitted"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestFilter_TeeWritesFullInputAndMarkerReferencesIt(t *testing.T) {
	dir := t.TempDir()
	teePath := filepath.Join(dir, "full.log")
	in := numbered(500)
	out, err := run([]string{"--budget-bytes", "400", "--head", "3", "--tail", "3", "--tee", teePath}, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	full, err := os.ReadFile(teePath)
	if err != nil {
		t.Fatalf("tee file not written: %v", err)
	}
	if string(full) != in {
		t.Fatalf("tee file is not the full input (got %d bytes, want %d)", len(full), len(in))
	}
	if !strings.Contains(out, "(full: "+teePath+")") {
		t.Fatalf("marker does not reference the tee path:\n%s", out)
	}
}

func TestFilter_InvalidRegexIsUsageError(t *testing.T) {
	_, err := run([]string{"--match", "("}, "whatever\n")
	assertExitCode(t, err, codeUsage)
}

func TestFilter_NegativeBudgetIsUsageError(t *testing.T) {
	_, err := run([]string{"--budget-bytes", "-1"}, "whatever\n")
	assertExitCode(t, err, codeUsage)
}

func TestFilter_TeeWriteFailureIsInternalError(t *testing.T) {
	// A --tee path whose parent directory does not exist makes os.WriteFile fail
	// deterministically, exercising internalErr and the exit-3 (internal/IO) half
	// of the exit-code contract — the only half no other test covers. stdout must
	// stay empty so a downstream pipe never receives a partial result.
	unwritable := filepath.Join(t.TempDir(), "nope", "full.log")
	out, err := run([]string{"--tee", unwritable}, "a\nb\nc\n")
	assertExitCode(t, err, codeInternal)
	if out != "" {
		t.Fatalf("stdout must be empty when --tee fails, got %q", out)
	}
}

// runExecute drives the real package entry point, cli.Execute(), with the process
// streams swapped, returning the exit code and captured stdout/stderr. Unlike the
// buffer-based run() helper (which calls newRootCmd directly), this exercises
// Execute()'s error -> exit-code mapping and its os.Stderr diagnostic routing.
func runExecute(t *testing.T, args []string, stdin string) (code int, stdout, stderr string) {
	t.Helper()
	origArgs, origIn, origOut, origErr := os.Args, os.Stdin, os.Stdout, os.Stderr
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() {
		os.Args, os.Stdin, os.Stdout, os.Stderr = origArgs, origIn, origOut, origErr
		inR.Close()
		outR.Close()
		errR.Close()
	})
	go func() {
		_, _ = io.WriteString(inW, stdin)
		inW.Close()
	}()

	os.Args = append([]string{"pare"}, args...)
	os.Stdin, os.Stdout, os.Stderr = inR, outW, errW
	code = Execute()
	outW.Close()
	errW.Close()
	ob, _ := io.ReadAll(outR)
	eb, _ := io.ReadAll(errR)
	return code, string(ob), string(eb)
}

func TestExecute_ContractAndExitMapping(t *testing.T) {
	t.Run("valid subcommand exits ok on stdout", func(t *testing.T) {
		code, out, _ := runExecute(t, []string{"version"}, "")
		if code != codeOK {
			t.Fatalf("exit = %d, want %d", code, codeOK)
		}
		if !strings.HasPrefix(out, "pare ") {
			t.Fatalf("version output should go to stdout: %q", out)
		}
	})
	t.Run("validation error -> exit 2, diagnostic on stderr, stdout pure", func(t *testing.T) {
		// Invalid regex is an *exitError(codeUsage): covers the errors.As branch.
		code, out, errb := runExecute(t, []string{"--match", "("}, "data\n")
		if code != codeUsage {
			t.Fatalf("exit = %d, want %d", code, codeUsage)
		}
		if out != "" {
			t.Fatalf("stdout must stay pure on error, got %q", out)
		}
		if !strings.Contains(errb, "pare:") {
			t.Fatalf("diagnostic must go to stderr, got %q", errb)
		}
	})
	t.Run("unknown flag -> bare cobra error maps to usage", func(t *testing.T) {
		// A plain cobra flag error is not an *exitError: covers the codeUsage fallback.
		code, _, errb := runExecute(t, []string{"--nope"}, "")
		if code != codeUsage {
			t.Fatalf("exit = %d, want %d", code, codeUsage)
		}
		if !strings.Contains(errb, "pare:") {
			t.Fatalf("diagnostic must go to stderr, got %q", errb)
		}
	})
}

func TestFilter_CustomMatchReplacesDefault(t *testing.T) {
	// No default error words; a unique token marks the line we want kept.
	var b strings.Builder
	for i := 1; i <= 200; i++ {
		if i == 100 {
			b.WriteString("here-be-dragons marker line\n")
		} else {
			fmt.Fprintf(&b, "filler %04d\n", i)
		}
	}
	out, err := run([]string{"--budget-bytes", "300", "--head", "2", "--tail", "2", "--context", "0", "--match", "dragons"}, b.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "here-be-dragons marker line") {
		t.Fatalf("custom --match line not kept:\n%s", out)
	}
}

func TestFilter_ProfileTestKeepsAssertionBlock(t *testing.T) {
	// Realistic go-test output: many passing tests around one failure whose
	// assertion body is indented under the `--- FAIL:` header.
	var b strings.Builder
	emit := func(from, to int) {
		for i := from; i <= to; i++ {
			fmt.Fprintf(&b, "=== RUN   TestPass%03d\n--- PASS: TestPass%03d (0.00s)\n", i, i)
		}
	}
	emit(1, 60)
	b.WriteString("--- FAIL: TestBeta (0.00s)\n")
	b.WriteString("    beta_test.go:42: got 3, want 4\n")
	b.WriteString("        values differ at index 0\n")
	emit(61, 120)
	b.WriteString("FAIL\texample/pkg\t0.123s\n")

	out, err := run([]string{"--profile", "test", "--budget-bytes", "600", "--head", "3", "--tail", "3", "--context", "0"}, b.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"beta_test.go:42: got 3, want 4", "values differ at index 0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("--profile test dropped assertion detail %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "TestPass030") {
		t.Fatalf("a middle passing test should be omitted:\n%s", out)
	}
}

func TestFilter_ProfileTestKeepsSwiftXCTestFailure(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 200; i++ {
		if i == 100 {
			b.WriteString("/repo/Tests/FooTests.swift:12: error: -[FooTests testBar] : XCTAssertEqual failed: (\"1\") is not equal to (\"2\")\n")
		} else {
			fmt.Fprintf(&b, "Test Case '-[FooTests testPass%03d]' passed (0.001 seconds).\n", i)
		}
	}
	out, err := run([]string{"--profile", "test", "--budget-bytes", "800", "--head", "2", "--tail", "2", "--context", "0"}, b.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "XCTAssertEqual failed") {
		t.Fatalf("--profile test dropped the XCTest failure line:\n%s", out)
	}
	if strings.Contains(out, "testPass050") {
		t.Fatalf("a passing case should be omitted:\n%s", out)
	}
}

func TestFilter_UnknownProfileIsUsageError(t *testing.T) {
	_, err := run([]string{"--profile", "nope"}, "whatever\n")
	assertExitCode(t, err, codeUsage)
}

func TestFilter_ProfileTestBlockExtentAppliesToCustomMatch(t *testing.T) {
	// Documented interaction: with --profile test an explicit --match still wins
	// as the matcher, but the profile's block extent applies around those matches
	// — so the indented body under a custom-matched line survives context 0.
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "pass line %02d\n", i)
	}
	b.WriteString("MYTOKEN header line\n")
	b.WriteString("    indented body under my token\n")
	b.WriteString("    second indented body line\n")
	for i := 41; i <= 80; i++ {
		fmt.Fprintf(&b, "pass line %02d\n", i)
	}
	out, err := run([]string{"--profile", "test", "--match", "MYTOKEN", "--budget-bytes", "400", "--head", "1", "--tail", "1", "--context", "0"}, b.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "second indented body line") {
		t.Fatalf("block extent should apply around a custom --match under --profile test:\n%s", out)
	}
	if strings.Contains(out, "pass line 20") {
		t.Fatalf("a middle pass line should be omitted:\n%s", out)
	}
}

func TestVersion_JSON(t *testing.T) {
	out, err := run([]string{"version", "--json"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var info struct {
		Version string `json:"version"`
		Go      string `json:"go"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &info); err != nil {
		t.Fatalf("version --json is not valid JSON: %v\n%s", err, out)
	}
	if info.Version == "" || info.Go == "" {
		t.Fatalf("version JSON missing fields: %s", out)
	}
}

func TestVersion_Human(t *testing.T) {
	out, err := run([]string{"version"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(out, "pare ") {
		t.Fatalf("human version should start with 'pare ': %q", out)
	}
}

func TestVersionFlag(t *testing.T) {
	out, err := run([]string{"--version"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(out, "pare ") {
		t.Fatalf("--version should print 'pare ...': %q", out)
	}
}

func assertExitCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error with exit code %d, got nil", want)
	}
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("error is not an *exitError: %v", err)
	}
	if ee.code != want {
		t.Fatalf("exit code = %d, want %d (err: %v)", ee.code, want, err)
	}
}

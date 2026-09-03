package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/akira-toriyama/pare/internal/budget"
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

// runExecute drives execute, the body of the package entry point cli.Execute(),
// on in-memory streams. Unlike the buffer-based run() helper (which calls
// newRootCmd directly and returns the error), this pins the error -> exit-code
// mapping and the routing of diagnostics to stderr.
func runExecute(args []string, stdin string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = execute(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func TestExecute_ContractAndExitMapping(t *testing.T) {
	t.Run("valid subcommand exits ok on stdout", func(t *testing.T) {
		code, out, _ := runExecute([]string{"version"}, "")
		if code != codeOK {
			t.Fatalf("exit = %d, want %d", code, codeOK)
		}
		if !strings.HasPrefix(out, "pare ") {
			t.Fatalf("version output should go to stdout: %q", out)
		}
	})
	t.Run("validation error -> exit 2, diagnostic on stderr, stdout pure", func(t *testing.T) {
		// Invalid regex is an *exitError(codeUsage): covers the errors.As branch.
		code, out, errb := runExecute([]string{"--match", "("}, "data\n")
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
		code, _, errb := runExecute([]string{"--nope"}, "")
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

func TestResolveOptions(t *testing.T) {
	// Validation and profile/matcher resolution are pure, so one table covers
	// every usage error and the documented --profile / --match interaction
	// without touching cobra or a stdin. Exit-code routing of these errors is
	// pinned separately by TestExecute_ContractAndExitMapping.
	defaults := filterConfig{budgetBytes: 8192, head: 15, tail: 15, context: 2}
	with := func(mut func(c *filterConfig)) filterConfig { c := defaults; mut(&c); return c }
	cases := []struct {
		name     string
		cfg      filterConfig
		wantErr  string // substring of the usage error; "" means success
		wantPats []string
		wantExt  budget.Extent
	}{
		{name: "generic defaults", cfg: defaults, wantPats: []string{budget.DefaultPattern}, wantExt: budget.ExtentLine},
		{name: "profile test", cfg: with(func(c *filterConfig) { c.profile = "test" }), wantPats: []string{budget.TestPattern}, wantExt: budget.ExtentBlock},
		{name: "profile test keeps block extent under explicit match", cfg: with(func(c *filterConfig) { c.profile = "test"; c.match = []string{"MYTOKEN"} }), wantPats: []string{"MYTOKEN"}, wantExt: budget.ExtentBlock},
		{name: "several matches are all compiled", cfg: with(func(c *filterConfig) { c.match = []string{"ALPHA", "OMEGA"} }), wantPats: []string{"ALPHA", "OMEGA"}, wantExt: budget.ExtentLine},
		{name: "negative budget", cfg: with(func(c *filterConfig) { c.budgetBytes = -1 }), wantErr: "must be >= 0"},
		{name: "negative head", cfg: with(func(c *filterConfig) { c.head = -1 }), wantErr: "must be >= 0"},
		{name: "negative tail", cfg: with(func(c *filterConfig) { c.tail = -1 }), wantErr: "must be >= 0"},
		{name: "negative context", cfg: with(func(c *filterConfig) { c.context = -1 }), wantErr: "must be >= 0"},
		{name: "invalid regex", cfg: with(func(c *filterConfig) { c.match = []string{"("} }), wantErr: `invalid --match regex "("`},
		{name: "unknown profile lists the registry", cfg: with(func(c *filterConfig) { c.profile = "nope" }), wantErr: `unknown --profile "nope" (known: ` + strings.Join(budget.ProfileNames(), ", ") + ")"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := resolveOptions(tc.cfg)
			if tc.wantErr != "" {
				assertExitCode(t, err, codeUsage)
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if opts.BudgetBytes != tc.cfg.budgetBytes || opts.Head != tc.cfg.head || opts.Tail != tc.cfg.tail || opts.Context != tc.cfg.context || opts.TeePath != tc.cfg.tee {
				t.Fatalf("numeric/tee fields not carried over: %+v from %+v", opts, tc.cfg)
			}
			if opts.Extent != tc.wantExt {
				t.Fatalf("extent = %v, want %v", opts.Extent, tc.wantExt)
			}
			var got []string
			for _, re := range opts.Matchers {
				got = append(got, re.String())
			}
			if strings.Join(got, "\x00") != strings.Join(tc.wantPats, "\x00") {
				t.Fatalf("matchers = %q, want %q", got, tc.wantPats)
			}
		})
	}
}

func TestHelp_ReportsBudgetDefaultsAndProfiles(t *testing.T) {
	// --help is the user-facing statement of the defaults; it must be seeded
	// from budget.Defaults() and budget.ProfileHelp(), never from literals that
	// can drift from the core.
	out, err := run([]string{"--help"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	d := budget.Defaults()
	for _, want := range []string{
		fmt.Sprintf("--budget-bytes int .*(default %d)", d.BudgetBytes),
		fmt.Sprintf("--head int .*(default %d)", d.Head),
		fmt.Sprintf("--tail int .*(default %d)", d.Tail),
		fmt.Sprintf("--context int .*(default %d)", d.Context),
		"--profile string .*extraction profile: " + budget.ProfileHelp(),
	} {
		// cobra pads flag columns; `.*` in want survives QuoteMeta as a wildcard.
		re := regexp.MustCompile(strings.ReplaceAll(regexp.QuoteMeta(want), `\.\*`, `.*`))
		if !re.MatchString(out) {
			t.Fatalf("--help missing %q:\n%s", want, out)
		}
	}
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

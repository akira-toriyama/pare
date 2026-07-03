package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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

// Package cli is pare's cobra adapter: it parses flags, reads stdin, drives the
// pure internal/budget core, and writes the trimmed result to stdout. It holds
// no truncation logic — that all lives in internal/budget. main is just
// os.Exit(cli.Execute()).
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/akira-toriyama/pare/internal/budget"
	"github.com/akira-toriyama/pare/internal/version"
	"github.com/spf13/cobra"
)

// pare's exit-code contract (documented in README): 0 ok / 2 usage or
// validation / 3 internal or I/O. There is no "upstream failed" code — pare is
// a filter and never sees the producer's exit status (that is what
// `set -o pipefail` is for; see the README).
const (
	codeOK       = 0
	codeUsage    = 2
	codeInternal = 3
)

// exitError couples an error with the exit code pare should return for it.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func usageErr(format string, a ...any) error {
	return &exitError{code: codeUsage, err: fmt.Errorf(format, a...)}
}

func internalErr(format string, a ...any) error {
	return &exitError{code: codeInternal, err: fmt.Errorf(format, a...)}
}

// Execute builds the root command, runs it, and maps the outcome to pare's
// exit-code contract. Errors are printed to stderr (never stdout), so a
// downstream `| jq` or `| grep` on stdout is never polluted by diagnostics.
func Execute() int {
	root := newRootCmd()
	err := root.Execute()
	if err == nil {
		return codeOK
	}
	fmt.Fprintln(os.Stderr, "pare: "+err.Error())
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	// A bare error here is a cobra flag/usage problem → usage by contract.
	return codeUsage
}

func newRootCmd() *cobra.Command {
	var cfg filterConfig

	root := &cobra.Command{
		Use:   "pare",
		Short: "Trim command output to a byte budget, keeping head, tail, and error lines",
		Long: "pare — context-budget-aware output truncation for AI coding agents.\n\n" +
			"Reads stdin and writes a trimmed version to stdout that fits within a byte\n" +
			"budget, keeping the first lines (head), the last lines (tail), and any lines\n" +
			"matching error patterns (with surrounding context) — the middle a blind\n" +
			"`| tail` would throw away. Gaps are shown as `[... N lines omitted ...]`.\n\n" +
			"pare is a filter: it never sees the producer's exit status, so pipe stderr in\n" +
			"with `2>&1` and use `set -o pipefail` if the upstream exit code matters.",
		Example: "  # keep a build's errors visible instead of a blind tail\n" +
			"  npm run build 2>&1 | pare\n\n" +
			"  # test runs: keep the whole failing assertion block, collapse passes\n" +
			"  go test -v ./... 2>&1 | pare --profile test\n\n" +
			"  # tighter budget, capture the full log, add an extra matcher\n" +
			"  make 2>&1 | pare --budget-bytes 4096 --tee /tmp/build.log --match WARN\n\n" +
			"  # keep upstream failure visible to the shell\n" +
			"  set -o pipefail; go test ./... 2>&1 | pare",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runFilter(cmd, cfg)
		},
	}

	// Bind flags straight to the config cobra hands runFilter, so filterConfig is
	// the single source of truth for these fields (no parallel var block + copy).
	root.Flags().IntVar(&cfg.budgetBytes, "budget-bytes", 8192, "byte ceiling for the output")
	root.Flags().IntVar(&cfg.head, "head", 15, "lines to always keep from the top")
	root.Flags().IntVar(&cfg.tail, "tail", 15, "lines to always keep from the bottom")
	root.Flags().IntVar(&cfg.context, "context", 2, "lines of context to keep around each matched line")
	root.Flags().StringArrayVar(&cfg.match, "match", nil, "error-line regex (repeatable); defaults to a built-in error pattern")
	root.Flags().StringVar(&cfg.profile, "profile", "", "extraction profile: 'test' tunes matching for test-runner failures and keeps the whole indented assertion block; empty = generic")
	root.Flags().StringVar(&cfg.tee, "tee", "", "write the full, untruncated input to this file and reference it in markers")

	root.Version = version.Get().Human()
	root.SetVersionTemplate("pare {{.Version}}\n")

	root.AddCommand(newVersionCmd())
	return root
}

type filterConfig struct {
	budgetBytes int
	head        int
	tail        int
	context     int
	match       []string
	profile     string
	tee         string
}

func runFilter(cmd *cobra.Command, cfg filterConfig) error {
	if cfg.budgetBytes < 0 || cfg.head < 0 || cfg.tail < 0 || cfg.context < 0 {
		return usageErr("--budget-bytes, --head, --tail and --context must be >= 0")
	}

	// A profile seeds the default matcher and the extent policy. An explicit
	// --match still wins as the matcher; the profile's extent applies either way.
	extent := budget.ExtentLine
	defaultPattern := budget.DefaultPattern
	switch cfg.profile {
	case "":
		// generic (head/tail + error-word matching, single-line extent)
	case "test":
		extent = budget.ExtentBlock
		defaultPattern = budget.TestPattern
	default:
		return usageErr("unknown --profile %q (known: test)", cfg.profile)
	}

	patterns := cfg.match
	if len(patterns) == 0 {
		patterns = []string{defaultPattern}
	}
	matchers := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return usageErr("invalid --match regex %q: %v", p, err)
		}
		matchers = append(matchers, re)
	}

	input, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return internalErr("reading stdin: %v", err)
	}

	if cfg.tee != "" {
		if err := os.WriteFile(cfg.tee, input, 0o644); err != nil {
			return internalErr("writing --tee file %q: %v", cfg.tee, err)
		}
	}

	res := budget.Pare(input, budget.Options{
		BudgetBytes: cfg.budgetBytes,
		Head:        cfg.head,
		Tail:        cfg.tail,
		Context:     cfg.context,
		Matchers:    matchers,
		Extent:      extent,
		TeePath:     cfg.tee,
	})

	if _, err := cmd.OutOrStdout().Write(res.Output); err != nil {
		return internalErr("writing stdout: %v", err)
	}
	return nil
}

package budget

import "strings"

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

// Profile preselects the matcher and the Extent for one kind of input. The
// pairing is this package's knowledge, not the CLI's: the CLI only looks a
// name up and reports Names in its usage text. A profile never changes the
// budget policy, only which lines are selected.
type Profile struct {
	Name    string // flag value; "" is the generic profile
	Doc     string // one clause for --help, after the quoted name
	Pattern string // matcher used when the caller supplies none
	Extent  Extent
}

// profiles is the registry in help order. The generic profile stays first and
// unnamed so a caller passing no profile lands on it.
var profiles = []Profile{
	{Name: "", Pattern: DefaultPattern, Extent: ExtentLine},
	{
		Name:    "test",
		Doc:     "tunes matching for test-runner failures and keeps the whole indented assertion block",
		Pattern: TestPattern,
		Extent:  ExtentBlock,
	},
}

// LookupProfile returns the profile registered under name; "" is the generic
// profile (DefaultPattern + ExtentLine).
func LookupProfile(name string) (Profile, bool) {
	for _, p := range profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// ProfileNames lists the named profiles in help order — the values a caller
// may pass besides the generic "".
func ProfileNames() []string {
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		if p.Name != "" {
			names = append(names, p.Name)
		}
	}
	return names
}

// ProfileHelp renders the named profiles for flag usage text:
//
//	'test' tunes matching …; empty = generic
func ProfileHelp() string {
	var parts []string
	for _, p := range profiles {
		if p.Name != "" {
			parts = append(parts, "'"+p.Name+"' "+p.Doc)
		}
	}
	return strings.Join(parts, "; ") + "; empty = generic"
}

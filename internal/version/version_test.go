package version

import (
	"runtime"
	"testing"
)

// Human is pure formatting over the Info fields (including Go), so constructing
// Info directly lets us pin every branch to an exact string.
func TestInfo_Human(t *testing.T) {
	cases := []struct {
		name string
		in   Info
		want string
	}{
		{
			name: "version only",
			in:   Info{Version: "dev", Go: "go1.25"},
			want: "dev go1.25",
		},
		{
			name: "commit, no date",
			in:   Info{Version: "dev", Commit: "abc123", Go: "go1.25"},
			want: "dev (abc123) go1.25",
		},
		{
			name: "commit exactly 12 chars is not truncated",
			in:   Info{Version: "v1.2.3", Commit: "abcdef012345", Go: "go1.25"},
			want: "v1.2.3 (abcdef012345) go1.25",
		},
		{
			name: "commit longer than 12 chars is truncated to 12",
			in:   Info{Version: "v1.2.3", Commit: "0123456789abcdef", Go: "go1.25"},
			want: "v1.2.3 (0123456789ab) go1.25",
		},
		{
			name: "commit and date",
			in:   Info{Version: "v1.2.3", Commit: "abcdef012345", Date: "2026-01-01", Go: "go1.25"},
			want: "v1.2.3 (abcdef012345, 2026-01-01) go1.25",
		},
		{
			name: "date only, no commit",
			in:   Info{Version: "v1.2.3", Date: "2026-01-01", Go: "go1.25"},
			want: "v1.2.3 (2026-01-01) go1.25",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.in.Human(); got != c.want {
				t.Fatalf("Human() = %q, want %q", got, c.want)
			}
		})
	}
}

// Get returns linker-injected values verbatim, skipping the VCS fallback entirely
// when Version/Commit/Date are all set.
func TestGet_LinkerValuesWinOverVCS(t *testing.T) {
	restore(t)
	Version, Commit, Date = "v9.9.9", "deadbeefcafef00d", "2030-12-31T00:00:00Z"
	got := Get()
	if got.Version != "v9.9.9" || got.Commit != "deadbeefcafef00d" || got.Date != "2030-12-31T00:00:00Z" {
		t.Fatalf("injected build identity must win verbatim: %+v", got)
	}
	if got.Go != runtime.Version() {
		t.Fatalf("Go = %q, want %q", got.Go, runtime.Version())
	}
}

// With the source-build defaults (Version "dev", empty Commit/Date) Get consults
// runtime/debug for VCS stamps. Whether the test binary carries them is toolchain
// dependent, so we only assert it never panics, keeps Version, and reports Go.
func TestGet_DefaultsReportGoAndDoNotPanic(t *testing.T) {
	restore(t)
	Version, Commit, Date = "dev", "", ""
	got := Get()
	if got.Version != "dev" {
		t.Fatalf("Version should stay %q, got %q", "dev", got.Version)
	}
	if got.Go == "" {
		t.Fatalf("Go must always be populated: %+v", got)
	}
}

// restore snapshots the package-level build vars and reinstates them after the
// test, so cases that overwrite them do not leak into others.
func restore(t *testing.T) {
	t.Helper()
	v, c, d := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = v, c, d })
}

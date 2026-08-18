package version

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	oldStore = "/nix/store/9jj3g3iq-gt-1.0.0/bin/gt"
	newStore = "/nix/store/57g9y8sy-gt-1.0.0/bin/gt"
)

func nixProfile(resolved string) []InstallCandidate {
	return []InstallCandidate{{Location: "/home/u/.nix-profile/bin/gt", Resolved: resolved}}
}

func TestClassifyBinarySkew(t *testing.T) {
	tests := []struct {
		name         string
		running      string
		pathPath     string
		candidates   []InstallCandidate
		pathDirs     []string
		superseded   bool
		pathShadowed bool
		onPath       bool
		skipped      bool
	}{
		{
			name:       "running the installed binary",
			running:    newStore,
			pathPath:   newStore,
			candidates: nixProfile(newStore),
			pathDirs:   []string{filepath.Dir(newStore)},
			onPath:     true,
		},
		{
			// The gt-3pk case: a long-lived session pinned the store path of
			// the build that was current when it started, so both the running
			// binary and `which gt` point at superseded code.
			name:         "session pinned a superseded store path",
			running:      oldStore,
			pathPath:     oldStore,
			candidates:   nixProfile(newStore),
			pathDirs:     []string{filepath.Dir(oldStore), "/home/u/.nix-profile/bin"},
			superseded:   true,
			pathShadowed: true,
			onPath:       true,
		},
		{
			// A daemon that started before the upgrade resolved the profile
			// symlink at exec time, so its exe is superseded even though its
			// directory was never on PATH.
			name:       "long-lived process off PATH is still superseded",
			running:    oldStore,
			pathPath:   newStore,
			candidates: nixProfile(newStore),
			pathDirs:   []string{"/home/u/.nix-profile/bin"},
			superseded: true,
		},
		{
			// Reported verbatim so callers can tell a deliberate build run
			// from a pinned session and stay quiet about the former.
			name:       "deliberate build output is not on PATH",
			running:    "/home/u/gastown/gt-build/gt",
			pathPath:   newStore,
			candidates: nixProfile(newStore),
			pathDirs:   []string{"/home/u/.nix-profile/bin"},
			superseded: true,
		},
		{
			// Two install mechanisms are in use (nix profile and make
			// install). Running either one is correct; only a binary matching
			// neither is skew.
			name:     "second install location also counts as installed",
			running:  "/home/u/.local/bin/gt",
			pathPath: "/home/u/.local/bin/gt",
			candidates: []InstallCandidate{
				{Location: "/home/u/.nix-profile/bin/gt", Resolved: newStore},
				{Location: "/home/u/.local/bin/gt", Resolved: "/home/u/.local/bin/gt"},
			},
			pathDirs: []string{"/home/u/.local/bin"},
			onPath:   true,
		},
		{
			// A test binary or `go run` output is not a gt install gone wrong.
			name:       "non-gt executable is not superseded",
			running:    "/tmp/go-build123/b001/cmd.test",
			pathPath:   newStore,
			candidates: nixProfile(newStore),
		},
		{
			name:       "no install location found",
			running:    newStore,
			pathPath:   newStore,
			candidates: nil,
			skipped:    true,
		},
		{
			name:       "running executable unknown",
			running:    "",
			candidates: nixProfile(newStore),
			skipped:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyBinarySkew(tt.running, tt.pathPath, tt.candidates, tt.pathDirs)

			if got.Skipped != tt.skipped {
				t.Fatalf("Skipped = %v, want %v (reason %q)", got.Skipped, tt.skipped, got.SkipReason)
			}
			if tt.skipped {
				if got.SkipReason == "" {
					t.Error("skipped check has no SkipReason")
				}
				return
			}
			if got.Superseded != tt.superseded {
				t.Errorf("Superseded = %v, want %v", got.Superseded, tt.superseded)
			}
			if got.PathShadowed != tt.pathShadowed {
				t.Errorf("PathShadowed = %v, want %v", got.PathShadowed, tt.pathShadowed)
			}
			if got.RunningOnPath != tt.onPath {
				t.Errorf("RunningOnPath = %v, want %v", got.RunningOnPath, tt.onPath)
			}
		})
	}
}

func TestBinarySkewDescribeAndDetails(t *testing.T) {
	superseded := classifyBinarySkew(oldStore, oldStore, nixProfile(newStore), []string{filepath.Dir(oldStore)})

	// The whole point of the message is that both paths are visible: the
	// operator needs to see that "which gt" and the install disagree.
	desc := superseded.Describe()
	for _, want := range []string{oldStore, newStore} {
		if !strings.Contains(desc, want) {
			t.Errorf("Describe() = %q, missing %q", desc, want)
		}
	}
	if len(superseded.Details()) == 0 {
		t.Error("superseded skew produced no details")
	}

	healthy := classifyBinarySkew(newStore, newStore, nixProfile(newStore), []string{filepath.Dir(newStore)})
	if len(healthy.Details()) != 0 {
		t.Errorf("healthy skew produced details: %v", healthy.Details())
	}
}

func TestInstalledCandidatesSkipsMissingLocations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := installedCandidates(); len(got) != 0 {
		t.Fatalf("expected no candidates in an empty home, got %v", got)
	}

	local := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "gt"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := installedCandidates()
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %v", got)
	}
	if got[0].Location != filepath.Join(local, "gt") {
		t.Errorf("Location = %q, want %q", got[0].Location, filepath.Join(local, "gt"))
	}
}

func TestScanSupersededProcessesFindsSelf(t *testing.T) {
	if _, err := os.Stat("/proc/self/exe"); err != nil {
		t.Skip("no /proc on this platform")
	}
	// The test binary is not named "gt", so a scan against any installed path
	// must not report it — this guards the basename filter that keeps the
	// scan from indicting unrelated processes.
	procs, err := ScanSupersededProcesses([]string{"/nonexistent/gt"})
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	self := os.Getpid()
	for _, p := range procs {
		if p.PID == self {
			t.Errorf("scan reported the test binary itself: %+v", p)
		}
	}
}

func TestGroupByExe(t *testing.T) {
	got := GroupByExe([]SupersededProcess{
		{PID: 1, Exe: oldStore},
		{PID: 2, Exe: oldStore},
		{PID: 3, Exe: "/home/u/gt-build/gt"},
	})

	if len(got) != 2 {
		t.Fatalf("expected 2 groups, got %+v", got)
	}
	if got[0].Exe != oldStore || len(got[0].PIDs) != 2 {
		t.Errorf("first group = %+v, want 2 pids on %s", got[0], oldStore)
	}
	if got[1].Exe != "/home/u/gt-build/gt" || len(got[1].PIDs) != 1 {
		t.Errorf("second group = %+v", got[1])
	}
	if GroupByExe(nil) != nil {
		t.Error("empty scan should produce no groups")
	}
}

func TestClassifyBinarySkewCollectsAllInstallPaths(t *testing.T) {
	candidates := []InstallCandidate{
		{Location: "/home/u/.nix-profile/bin/gt", Resolved: newStore},
		{Location: "/home/u/.local/bin/gt", Resolved: "/home/u/.local/bin/gt"},
	}
	got := classifyBinarySkew(newStore, newStore, candidates, nil)

	// The process scan needs every install path: a daemon launched from
	// ~/.local/bin is not superseded just because the profile is preferred.
	if len(got.InstalledPaths) != 2 {
		t.Fatalf("InstalledPaths = %v, want both candidates", got.InstalledPaths)
	}
}

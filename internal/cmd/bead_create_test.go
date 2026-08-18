package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolveBeadCreate(t *testing.T) {
	townRoot := newBeadCreateTown(t)
	rigBeads := filepath.Join(townRoot, "gastown", "mayor", "rig", ".beads")
	townBeads := filepath.Join(townRoot, ".beads")
	townRootFn := func() (string, error) { return townRoot, nil }

	tests := []struct {
		name     string
		args     []string
		wantArgs []string
		wantDir  string
	}{
		{
			name:     "rig alias is stripped and pinned",
			args:     []string{"--repo", "gastown", "Fix auth"},
			wantArgs: []string{"Fix auth"},
			wantDir:  rigBeads,
		},
		{
			name:     "equals form is stripped and pinned",
			args:     []string{"--repo=gastown", "Fix auth", "-p", "1"},
			wantArgs: []string{"Fix auth", "-p", "1"},
			wantDir:  rigBeads,
		},
		{
			name:     "hq alias pins town beads",
			args:     []string{"Cross-rig thing", "--repo", "hq"},
			wantArgs: []string{"Cross-rig thing"},
			wantDir:  townBeads,
		},
		{
			name:     "no repo flag is passed through unpinned",
			args:     []string{"Local bug", "--type", "bug"},
			wantArgs: []string{"Local bug", "--type", "bug"},
			wantDir:  "",
		},
		{
			// bd resolves paths correctly on its own; rewriting them would be a
			// regression, not a fix.
			name:     "path repo is left to bd",
			args:     []string{"--repo", "/tmp/somewhere", "Bug"},
			wantArgs: []string{"--repo", "/tmp/somewhere", "Bug"},
			wantDir:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotArgs, gotDir, err := resolveBeadCreate(tc.args, townRootFn)
			if err != nil {
				t.Fatalf("resolveBeadCreate: %v", err)
			}
			if !reflect.DeepEqual(gotArgs, tc.wantArgs) {
				t.Errorf("args = %q, want %q", gotArgs, tc.wantArgs)
			}
			if gotDir != tc.wantDir {
				t.Errorf("beadsDir = %q, want %q", gotDir, tc.wantDir)
			}
		})
	}
}

// TestResolveBeadCreateUnknownAliasFailsLoudly is the regression guard for the
// actual defect: an unresolvable rig name must stop the create, not sail through
// to bd where it would exit 0 and print an ID for a bead that does not exist.
func TestResolveBeadCreateUnknownAliasFailsLoudly(t *testing.T) {
	townRoot := newBeadCreateTown(t)

	args, dir, err := resolveBeadCreate([]string{"--repo", "nosuchrig", "Lost bead"},
		func() (string, error) { return townRoot, nil })
	if err == nil {
		t.Fatalf("resolveBeadCreate accepted unknown alias: args=%q dir=%q", args, dir)
	}
	if args != nil || dir != "" {
		t.Errorf("failing resolve returned args=%q dir=%q, want nils", args, dir)
	}

	msg := err.Error()
	if !strings.Contains(msg, "nosuchrig") {
		t.Errorf("error does not name the bad alias: %v", err)
	}
	// The list is what makes the failure actionable rather than merely loud.
	if !strings.Contains(msg, "Known aliases:") || !strings.Contains(msg, "gastown") {
		t.Errorf("error does not list valid aliases: %v", err)
	}
}

func TestResolveBeadCreateOutsideTown(t *testing.T) {
	wantErr := errors.New("not in a town")

	// An alias needs a town to resolve against, so this must fail...
	if _, _, err := resolveBeadCreate([]string{"--repo", "gastown", "Bug"},
		func() (string, error) { return "", wantErr }); !errors.Is(err, wantErr) {
		t.Errorf("alias outside a town: err = %v, want wrapped %v", err, wantErr)
	}

	// ...but a plain create must not even look for one.
	if _, _, err := resolveBeadCreate([]string{"Bug"}, func() (string, error) {
		t.Fatal("town root looked up for a create with no --repo")
		return "", nil
	}); err != nil {
		t.Errorf("plain create outside a town: %v", err)
	}
}

// TestBuildBdCreateCmdPinsBeadsDir verifies the resolved target actually reaches
// bd as BEADS_DIR, and that an inherited value cannot shadow it.
func TestBuildBdCreateCmdPinsBeadsDir(t *testing.T) {
	t.Setenv("BEADS_DIR", "/inherited/wrong/.beads")
	target := filepath.Join(t.TempDir(), ".beads")

	c := buildBdCreateCmd([]string{"Title"}, target)

	// Collect every entry, not just the last: glibc getenv returns the FIRST
	// match, so a surviving inherited entry would win no matter what we append.
	var seen []string
	for _, entry := range c.Env {
		if v, ok := strings.CutPrefix(entry, "BEADS_DIR="); ok {
			seen = append(seen, v)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("BEADS_DIR appears %d times (%q), want exactly once", len(seen), seen)
	}
	if seen[0] != target {
		t.Errorf("BEADS_DIR = %q, want %q", seen[0], target)
	}

	wantArgs := []string{"create", "Title"}
	if !reflect.DeepEqual(c.Args[1:], wantArgs) {
		t.Errorf("bd args = %q, want %q", c.Args[1:], wantArgs)
	}
}

func newBeadCreateTown(t *testing.T) string {
	t.Helper()

	townRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	townBeads := filepath.Join(townRoot, ".beads")
	rigBeads := filepath.Join(townRoot, "gastown", "mayor", "rig", ".beads")
	for _, dir := range []string{townBeads, rigBeads} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	routes := `{"prefix":"hq-","path":"."}` + "\n" +
		`{"prefix":"gt-","path":"gastown/mayor/rig"}` + "\n"
	if err := os.WriteFile(filepath.Join(townBeads, "routes.jsonl"), []byte(routes), 0644); err != nil {
		t.Fatal(err)
	}
	return townRoot
}

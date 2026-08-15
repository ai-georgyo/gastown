package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// newTownWithRig builds a town root containing a rig directory that
// detectRigFromPath will recognize (it looks for <rig>/config.json).
func newTownWithRig(t *testing.T, rigNames ...string) string {
	t.Helper()
	townRoot := t.TempDir()
	for _, name := range rigNames {
		rigDir := filepath.Join(townRoot, name)
		if err := os.MkdirAll(rigDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rigDir, "config.json"), []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return townRoot
}

func TestResolveEventRig_TownLevelChannel(t *testing.T) {
	townRoot := newTownWithRig(t, "gastown")
	t.Chdir(filepath.Join(townRoot, "gastown"))

	// The mayor channel has one consumer for the whole town, so it stays
	// town-level even when emitted from inside a rig.
	rigName, err := resolveEventRig(townRoot, "", "mayor")
	if err != nil {
		t.Fatalf("resolveEventRig failed: %v", err)
	}
	if rigName != "" {
		t.Errorf("rig = %q, want empty for a town-level channel", rigName)
	}
}

func TestResolveEventRig_FromCwd(t *testing.T) {
	townRoot := newTownWithRig(t, "gastown")
	// A polecat worktree deep inside the rig still resolves to the rig.
	deep := filepath.Join(townRoot, "gastown", "polecats", "capable", "gastown")
	if err := os.MkdirAll(deep, 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(deep)
	t.Setenv("GT_RIG", "")

	rigName, err := resolveEventRig(townRoot, "", "refinery")
	if err != nil {
		t.Fatalf("resolveEventRig failed: %v", err)
	}
	if rigName != "gastown" {
		t.Errorf("rig = %q, want gastown", rigName)
	}
}

func TestResolveEventRig_ExplicitFlagWins(t *testing.T) {
	townRoot := newTownWithRig(t, "gastown", "avalon")
	t.Chdir(filepath.Join(townRoot, "gastown"))

	rigName, err := resolveEventRig(townRoot, "avalon", "refinery")
	if err != nil {
		t.Fatalf("resolveEventRig failed: %v", err)
	}
	if rigName != "avalon" {
		t.Errorf("rig = %q, want avalon", rigName)
	}
}

func TestResolveEventRig_EnvFallback(t *testing.T) {
	townRoot := newTownWithRig(t, "gastown")
	// Town root itself is not inside a rig — fall back to GT_RIG.
	t.Chdir(townRoot)
	t.Setenv("GT_RIG", "gastown")

	rigName, err := resolveEventRig(townRoot, "", "refinery")
	if err != nil {
		t.Fatalf("resolveEventRig failed: %v", err)
	}
	if rigName != "gastown" {
		t.Errorf("rig = %q, want gastown", rigName)
	}
}

// TestResolveEventRig_UnresolvableIsAnError guards the design decision behind
// gt-em1: a rig-scoped channel must never silently fall back to the shared
// town-level directory, because that is exactly the crosstalk being fixed.
func TestResolveEventRig_UnresolvableIsAnError(t *testing.T) {
	townRoot := newTownWithRig(t, "gastown")
	t.Chdir(townRoot)
	t.Setenv("GT_RIG", "")

	if _, err := resolveEventRig(townRoot, "", "refinery"); err == nil {
		t.Error("expected an error when the rig cannot be determined")
	}
}

func TestResolveEventRig_RejectsInvalidRig(t *testing.T) {
	townRoot := newTownWithRig(t, "gastown")

	if _, err := resolveEventRig(townRoot, "../etc", "refinery"); err == nil {
		t.Error("expected an error for a rig name with path traversal")
	}
}

func TestResolveEventDir_ScopesByRig(t *testing.T) {
	townRoot := newTownWithRig(t, "gastown")
	t.Chdir(filepath.Join(townRoot, "gastown"))
	t.Setenv("GT_RIG", "")

	dir, rigName, err := resolveEventDir(townRoot, "", "refinery")
	if err != nil {
		t.Fatalf("resolveEventDir failed: %v", err)
	}
	if rigName != "gastown" {
		t.Errorf("rig = %q, want gastown", rigName)
	}
	want := filepath.Join(townRoot, "events", "rigs", "gastown", "refinery")
	if dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}

	townDir, rigName, err := resolveEventDir(townRoot, "", "mayor")
	if err != nil {
		t.Fatalf("resolveEventDir(mayor) failed: %v", err)
	}
	if rigName != "" {
		t.Errorf("rig = %q, want empty for mayor", rigName)
	}
	if want := filepath.Join(townRoot, "events", "mayor"); townDir != want {
		t.Errorf("dir = %q, want %q", townDir, want)
	}
}

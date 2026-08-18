//go:build integration

// Integration coverage for `gt bead create --repo <rig>` (gt-789).
//
// Run with: go test -tags=integration ./internal/cmd -run TestBeadCreateRepoAlias -v
package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

var beadCreateTestCounter atomic.Int32

// TestBeadCreateRepoAliasLandsBeadFindableByTitle is the acceptance test for
// gt-789 and is written to be immune to the defect it covers.
//
// Raw `bd create --repo <rig>` exits 0 and prints a plausible ID for a bead it
// never wrote, and `bd show <that-id>` then prefix-matches onto some unrelated
// real bead and also exits 0 — so both obvious ways to check the work confirm a
// loss. This test therefore asserts nothing about the printed ID or about exit
// status alone. It searches the target database BY TITLE and fails if the bead
// is absent.
func TestBeadCreateRepoAliasLandsBeadFindableByTitle(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not installed, skipping bead create integration test")
	}
	requireDoltServer(t)

	n := beadCreateTestCounter.Add(1)
	rigPrefix := fmt.Sprintf("bc%d", n)
	townRoot := setupRoutingTestTownWithPrefixes(t, fmt.Sprintf("bh%d", n), rigPrefix, fmt.Sprintf("bt%d", n))
	rigDir := filepath.Join(townRoot, "gastown", "mayor", "rig")
	initBeadsDBWithPrefix(t, rigDir, rigPrefix)

	rigBeads := filepath.Join(rigDir, ".beads")
	title := fmt.Sprintf("gt-789 acceptance probe %d", n)

	// Resolve exactly as the command does, then run the command's own bd
	// invocation — no reimplementation of the path under test.
	createArgs, beadsDir, err := resolveBeadCreate(
		[]string{"--repo", "gastown", "--title", title, "--type", "task", "-p", "3"},
		func() (string, error) { return townRoot, nil },
	)
	if err != nil {
		t.Fatalf("resolveBeadCreate: %v", err)
	}
	if beadsDir != rigBeads {
		t.Fatalf("resolved beads dir = %q, want %q", beadsDir, rigBeads)
	}

	c := buildBdCreateCmd(createArgs, beadsDir)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("gt bead create --repo gastown failed: %v\n%s", err, out)
	}
	t.Logf("create output (deliberately not asserted on): %s", strings.TrimSpace(string(out)))

	// The only assertion that the defect cannot fake.
	if !beadTitleExists(t, rigBeads, title) {
		t.Fatalf("bead %q is absent from %s after a create that reported success\n%s",
			title, rigBeads, out)
	}
}

// TestBeadCreateUnknownRepoAliasWritesNothing covers the other half: when the rig
// name cannot be resolved, the create must be refused outright rather than handed
// to bd, which would discard it while reporting success.
func TestBeadCreateUnknownRepoAliasWritesNothing(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not installed, skipping bead create integration test")
	}
	requireDoltServer(t)

	n := beadCreateTestCounter.Add(1)
	rigPrefix := fmt.Sprintf("bc%d", n)
	townRoot := setupRoutingTestTownWithPrefixes(t, fmt.Sprintf("bh%d", n), rigPrefix, fmt.Sprintf("bt%d", n))
	rigDir := filepath.Join(townRoot, "gastown", "mayor", "rig")
	initBeadsDBWithPrefix(t, rigDir, rigPrefix)

	rigBeads := filepath.Join(rigDir, ".beads")
	title := fmt.Sprintf("gt-789 unknown-alias probe %d", n)

	_, _, err := resolveBeadCreate(
		[]string{"--repo", "nosuchrig", "--title", title, "--type", "task", "-p", "3"},
		func() (string, error) { return townRoot, nil },
	)
	if err == nil {
		t.Fatal("unknown repo alias was accepted; it must be refused before bd runs")
	}

	// Nothing may have leaked into the real rig either.
	if beadTitleExists(t, rigBeads, title) {
		t.Fatalf("bead %q was written despite the alias being unresolvable", title)
	}
}

// beadTitleExists searches a beads database by title. It reads the list output
// rather than looking an ID up, because ID lookup is the check the defect fools.
func beadTitleExists(t *testing.T, beadsDir, title string) bool {
	t.Helper()

	cmd := exec.Command("bd", "list", "--all")
	// Strip before appending: glibc getenv returns the FIRST match, so an
	// inherited BEADS_DIR would shadow ours and we would search the wrong
	// database — and then report the bead missing for the wrong reason.
	cmd.Env = append(beads.StripEnvKey(os.Environ(), "BEADS_DIR"), "BEADS_DIR="+beadsDir)
	cmd.Dir = filepath.Dir(beadsDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd list in %s: %v\n%s", beadsDir, err, out)
	}
	return strings.Contains(string(out), title)
}

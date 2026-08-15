package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/channelevents"
	"github.com/steveyegge/gastown/internal/workspace"
)

// The event bus (<town>/events/<channel>/) is live IPC: a file written there is
// read and acted on by whichever agent is subscribed to that channel. These
// tests pin the property that running this package's unit tests cannot put
// anything on it, which is stronger than "the one test that leaked no longer
// leaks" -- gt clones live inside the town they orchestrate, so any test that
// resolves a town root resolves the *live* one.

// newFakeTown creates a directory that workspace.Find recognises as a town root
// and makes it the working directory for the duration of the test.
func newFakeTown(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mayor"), 0755); err != nil {
		t.Fatalf("creating fake town: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "mayor", "town.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("writing town.json: %v", err)
	}

	t.Chdir(root)

	// Guard the premise: if the fake town is not what gets resolved, the
	// assertions below would pass without testing anything.
	resolved, err := workspace.FindFromCwd()
	if err != nil {
		t.Fatalf("resolving fake town root: %v", err)
	}
	if resolved == "" {
		t.Fatal("fake town root did not resolve; the test would prove nothing")
	}
	return resolved
}

// listEvents returns every path beneath <root>/events, so an assertion can be
// made about the directory itself rather than about the absence of a panic.
func listEvents(t *testing.T, root string) []string {
	t.Helper()

	eventsDir := filepath.Join(root, "events")
	var found []string
	err := filepath.WalkDir(eventsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(eventsDir, path)
		if relErr != nil {
			return relErr
		}
		if rel != "." {
			found = append(found, rel)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walking %s: %v", eventsDir, err)
	}
	return found
}

// TestNudgeHelpersEmitNoEventsUnderTest is the regression test for the leak:
// nudgeRefinery and nudgeWitness both resolve a town root and emit to it, and
// both are reachable from tests with GT_TEST_NUDGE_LOG cleared. Asserted on the
// events directory, which must not even come into existence.
func TestNudgeHelpersEmitNoEventsUnderTest(t *testing.T) {
	townRoot := newFakeTown(t)

	// Clear the log hook exactly as the leaking test did. An empty value used
	// to mean "take the real path", which reached the live event bus.
	t.Setenv("GT_TEST_NUDGE_LOG", "")

	nudgeRefinery("nonexistent-rig", "test message")
	nudgeWitness("nonexistent-rig", "test message")

	if leaked := listEvents(t, townRoot); len(leaked) > 0 {
		t.Errorf("nudge helpers wrote to the event bus under test: %v", leaked)
	}
	if _, err := os.Stat(filepath.Join(townRoot, "events")); !os.IsNotExist(err) {
		t.Errorf("events directory was created under test at %s (stat err: %v)",
			filepath.Join(townRoot, "events"), err)
	}
}

// TestEmitToResolvedTownIsBlockedUnderTest covers the general property rather
// than the two helpers: any code path that resolves a town root and emits is
// denied, and the denial is an error rather than a silent success.
func TestEmitToResolvedTownIsBlockedUnderTest(t *testing.T) {
	townRoot := newFakeTown(t)

	for _, channel := range []string{"refinery", "witness", "mayor"} {
		if _, err := channelevents.EmitToTown(townRoot, channel, "MQ_SUBMIT", []string{"source=sling"}); err == nil {
			t.Errorf("EmitToTown(%q) succeeded under test; the event bus must be sandboxed", channel)
		}
		if _, err := channelevents.Emit(channel, "MQ_SUBMIT", []string{"source=sling"}); err == nil {
			t.Errorf("Emit(%q) succeeded under test; the event bus must be sandboxed", channel)
		}
	}

	if leaked := listEvents(t, townRoot); len(leaked) > 0 {
		t.Errorf("blocked emissions still left files behind: %v", leaked)
	}
}

// TestLiveTownEventBusIsNotWritable asserts against the real town this test
// binary is running inside, which is the town that was actually being polluted.
// The sandbox must deny it without the test having to opt out of anything.
func TestLiveTownEventBusIsNotWritable(t *testing.T) {
	liveRoot, err := workspace.FindFromCwd()
	if err != nil || liveRoot == "" {
		t.Skip("not running inside a town; nothing to protect")
	}

	before := listEvents(t, liveRoot)

	path, err := channelevents.EmitToTown(liveRoot, "refinery", "MQ_SUBMIT", []string{
		"source=sling",
		"message=test message",
	})
	if err == nil {
		// The sandbox is broken and a live subscriber is about to act on this.
		// Retract it before reporting, so a failing test does not itself
		// become the leak it is reporting.
		if rmErr := os.Remove(path); rmErr != nil {
			t.Errorf("could not retract the leaked event %s: %v", path, rmErr)
		}
		t.Fatalf("emitted MQ_SUBMIT into the live town at %s", liveRoot)
	}

	// Directory-level check as well: a denial that still created the channel
	// directory would be a partial fix.
	after := listEvents(t, liveRoot)
	if len(after) != len(before) {
		t.Errorf("live town event bus changed during the test: before %v, after %v", before, after)
	}
}

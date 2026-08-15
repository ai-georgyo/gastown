package channelevents

// Test sandbox for the event bus.
//
// <town>/events/<channel>/ is live IPC, not storage: whatever lands there is
// picked up by the agent subscribed to that channel, in whatever town the
// process happens to resolve. A unit test that reaches an emit function is
// therefore not writing a file, it is injecting a command into a running town.
//
// The historical guard was an env var (GT_TEST_NUDGE_LOG) whose *empty* value
// meant "take the real path". A test that cleared it in order to exercise the
// non-test branch silently acquired write access to production -- which is how
// TestNudgeRefineryNoOpWithoutLog came to fire real MQ_SUBMIT events into the
// live town on every `go test ./internal/cmd/...`, and how a rig that had never
// submitted anything saw phantom merge-queue submits.
//
// This sandbox fails closed instead. Under `go test`, emission is denied unless
// the test has explicitly registered the town root it owns. There is no value a
// test can set to opt out: the only way to emit is to name a directory you
// control, and naming the real town root is a visible, reviewable act rather
// than an accident.
//
// Outside `go test` nothing changes, including for a real gt binary that an
// integration test launches as a subprocess -- that process is not built by
// `go test`, and sandboxing it is the job of the town root it is pointed at.

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// sandbox holds the town roots that tests have claimed ownership of.
// Roots are reference counted so that parallel tests and subtests sharing a
// root do not revoke each other's permission on cleanup.
var sandbox struct {
	mu    sync.RWMutex
	roots map[string]int
}

// AllowEmitForTest permits event emission beneath townRoot for the duration of
// tb, and reverts when tb finishes.
//
// townRoot must be a directory the test owns -- normally t.TempDir(). Passing a
// real town root would reintroduce exactly the leak this sandbox exists to
// prevent.
func AllowEmitForTest(tb testing.TB, townRoot string) {
	tb.Helper()
	if townRoot == "" {
		tb.Fatal("channelevents.AllowEmitForTest: townRoot must not be empty")
	}
	root := resolveExisting(townRoot)

	sandbox.mu.Lock()
	if sandbox.roots == nil {
		sandbox.roots = make(map[string]int)
	}
	sandbox.roots[root]++
	sandbox.mu.Unlock()

	tb.Cleanup(func() {
		sandbox.mu.Lock()
		defer sandbox.mu.Unlock()
		if sandbox.roots[root] <= 1 {
			delete(sandbox.roots, root)
			return
		}
		sandbox.roots[root]--
	})
}

// checkEmitAllowed reports whether eventDir may be written to. Outside of
// `go test` every emission is allowed; under test only directories beneath a
// root registered with AllowEmitForTest are.
//
// Callers must consult this *before* creating directories: MkdirAll on a live
// channel path is itself a write to the town.
func checkEmitAllowed(eventDir string) error {
	if !testing.Testing() {
		return nil
	}
	dir := resolveExisting(eventDir)

	sandbox.mu.RLock()
	defer sandbox.mu.RUnlock()
	for root := range sandbox.roots {
		if dir == root || strings.HasPrefix(dir, root+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("channelevents: refusing to emit to %s: the town event bus is not writable "+
		"from tests; emit into a town root the test owns and register it with "+
		"channelevents.AllowEmitForTest(t, townRoot)", eventDir)
}

// resolveExisting makes path absolute and resolves symlinks as far up as the
// path actually exists, then re-appends the missing tail. A registered root and
// an emission target that differ only in symlinked ancestry (/tmp vs
// /private/tmp on macOS) must compare equal, and the emission target usually
// does not exist yet at the time it is checked.
func resolveExisting(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}

	tail := ""
	cur := abs
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(resolved, tail)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		tail = filepath.Join(filepath.Base(cur), tail)
		cur = parent
	}
}

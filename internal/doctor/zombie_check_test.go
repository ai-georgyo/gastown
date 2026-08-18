package doctor

import (
	"testing"
)

func TestNewZombieSessionCheck(t *testing.T) {
	check := NewZombieSessionCheck()

	if check.Name() != "zombie-sessions" {
		t.Errorf("expected name 'zombie-sessions', got %q", check.Name())
	}

	if check.Description() != "Detect tmux sessions with dead Claude processes" {
		t.Errorf("expected description 'Detect tmux sessions with dead Claude processes', got %q", check.Description())
	}

	if !check.CanFix() {
		t.Error("expected CanFix to return true")
	}

	if check.Category() != CategoryCleanup {
		t.Errorf("expected category %q, got %q", CategoryCleanup, check.Category())
	}
}

func TestZombieSessionCheck_Run_NoSessions(t *testing.T) {
	// This test verifies the check runs without error.
	// Results depend on the test environment.
	check := NewZombieSessionCheck()
	ctx := &CheckContext{TownRoot: t.TempDir()}

	result := check.Run(ctx)

	// Should return OK or Warning depending on environment
	if result.Status != StatusOK && result.Status != StatusWarning {
		t.Errorf("expected StatusOK or StatusWarning, got %v: %s", result.Status, result.Message)
	}
}

func TestZombieSessionCheck_SkipsCrewSessions(t *testing.T) {
	setupTestRegistry(t)

	// Crew sessions must never be reported as zombies, even when the liveness
	// probe says no agent is running in them.
	term := newFakeTerminator()
	lister := &mockSessionLister{sessions: []string{"gt-crew-joe", "nif-crew-codex1"}}
	check := NewZombieSessionCheckWithDeps(lister, term)

	result := check.Run(&CheckContext{TownRoot: t.TempDir()})

	if len(check.zombieSessions) != 0 {
		t.Errorf("crew sessions were queued for cleanup: %v", check.zombieSessions)
	}
	if result.Status != StatusOK {
		t.Errorf("expected StatusOK, got %v: %s", result.Status, result.Message)
	}
}

func TestZombieSessionCheck_FixProtectsCrewSessions(t *testing.T) {
	setupTestRegistry(t)

	term := newFakeTerminator()
	check := NewZombieSessionCheckWithDeps(nil, term)

	// Manually set zombies including a crew session (simulating a bug upstream
	// in Run, which is exactly what the Fix-side safeguard exists to catch).
	check.zombieSessions = []string{
		"gt-crew-joe", // Should be skipped
		"gt-morsov",   // Ordinary polecat zombie - killed
	}

	if err := check.Fix(&CheckContext{TownRoot: t.TempDir()}); err != nil {
		t.Fatalf("Fix returned error: %v", err)
	}

	if term.wasKilled("gt-crew-joe") {
		t.Errorf("crew session was killed (killed: %v)", term.killed)
	}
	if !term.wasKilled("gt-morsov") {
		t.Errorf("ordinary zombie was not killed (killed: %v)", term.killed)
	}
}

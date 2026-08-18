package doctor

import (
	"errors"
	"testing"
)

// fakeTerminator records kill attempts instead of performing them, so tests can
// assert on the kill NOT happening rather than on a predicate's return value.
type fakeTerminator struct {
	alive    map[string]bool
	aliveErr map[string]error
	killed   []string
	killErr  error
}

func newFakeTerminator() *fakeTerminator {
	return &fakeTerminator{alive: map[string]bool{}, aliveErr: map[string]error{}}
}

func (f *fakeTerminator) IsAgentAliveChecked(sess string) (bool, error) {
	if err, ok := f.aliveErr[sess]; ok {
		return false, err
	}
	return f.alive[sess], nil
}

func (f *fakeTerminator) Kill(sess, reason string) error {
	f.killed = append(f.killed, sess)
	return f.killErr
}

func (f *fakeTerminator) wasKilled(sess string) bool {
	for _, k := range f.killed {
		if k == sess {
			return true
		}
	}
	return false
}

// TestZombieFix_DoesNotKillUnparseableSession is the control for gt-tdk.
// "hq-crew-joe" satisfies session.IsKnownSession (hq- prefix) but
// ParseSessionName rejects it, so it reaches the kill path. A guard whose
// entire job is protecting sessions must not fail open on the inputs it
// cannot understand.
func TestZombieFix_DoesNotKillUnparseableSession(t *testing.T) {
	setupTestRegistry(t)

	term := newFakeTerminator()
	lister := &mockSessionLister{sessions: []string{"hq-crew-joe"}}
	check := NewZombieSessionCheckWithDeps(lister, term)

	ctx := &CheckContext{TownRoot: t.TempDir()}
	check.Run(ctx)
	if err := check.Fix(ctx); err != nil {
		t.Fatalf("Fix returned error: %v", err)
	}

	if term.wasKilled("hq-crew-joe") {
		t.Errorf("unparseable session %q was killed; a kill-path guard must fail closed on names it cannot parse (killed: %v)",
			"hq-crew-joe", term.killed)
	}
}

// TestZombieFix_DoesNotKillLongFormCrewSession is the second control: the
// name the town feed shows being killed 36 times. It PARSES (as a polecat
// named "gastown-crew-joe"), so a parse-error guard alone does not save it —
// only a genuinely independent, registry-free reading of the name does.
func TestZombieFix_DoesNotKillLongFormCrewSession(t *testing.T) {
	setupTestRegistry(t)

	term := newFakeTerminator()
	lister := &mockSessionLister{sessions: []string{"gt-gastown-crew-joe"}}
	check := NewZombieSessionCheckWithDeps(lister, term)

	ctx := &CheckContext{TownRoot: t.TempDir()}
	check.Run(ctx)
	if err := check.Fix(ctx); err != nil {
		t.Fatalf("Fix returned error: %v", err)
	}

	if term.wasKilled("gt-gastown-crew-joe") {
		t.Errorf("crew-named session %q was killed (killed: %v)", "gt-gastown-crew-joe", term.killed)
	}
}

// TestZombieFix_DoesNotKillCrewWhenLivenessSaysDead asserts the crew guard is
// not conditional on the liveness probe: a crew session is protected even when
// the probe reports not-alive.
func TestZombieFix_DoesNotKillCrewWhenLivenessSaysDead(t *testing.T) {
	setupTestRegistry(t)

	term := newFakeTerminator()
	term.alive["gt-crew-joe"] = false // probe says dead

	check := NewZombieSessionCheckWithDeps(nil, term)
	check.zombieSessions = []string{"gt-crew-joe"} // as if Run had listed it

	if err := check.Fix(&CheckContext{TownRoot: t.TempDir()}); err != nil {
		t.Fatalf("Fix returned error: %v", err)
	}
	if term.wasKilled("gt-crew-joe") {
		t.Errorf("crew session was killed despite the crew guard (killed: %v)", term.killed)
	}
}

// TestZombieFix_DoesNotKillWhenLivenessProbeErrors covers scope item 3: the
// TOCTOU re-check must not resolve "I could not tell" toward destruction.
func TestZombieFix_DoesNotKillWhenLivenessProbeErrors(t *testing.T) {
	setupTestRegistry(t)

	term := newFakeTerminator()
	term.aliveErr["gt-witness"] = errors.New("tmux: no server running")

	check := NewZombieSessionCheckWithDeps(nil, term)
	check.zombieSessions = []string{"gt-witness"}

	if err := check.Fix(&CheckContext{TownRoot: t.TempDir()}); err != nil {
		t.Fatalf("Fix returned error: %v", err)
	}
	if term.wasKilled("gt-witness") {
		t.Errorf("session killed while its liveness was unknown (killed: %v)", term.killed)
	}
}

// TestZombieFix_KillsGenuineZombie is the positive control: the guards must
// not be so broad that real zombies stop being cleaned up.
func TestZombieFix_KillsGenuineZombie(t *testing.T) {
	setupTestRegistry(t)

	term := newFakeTerminator()
	term.alive["gt-morsov"] = false

	lister := &mockSessionLister{sessions: []string{"gt-morsov"}}
	check := NewZombieSessionCheckWithDeps(lister, term)

	ctx := &CheckContext{TownRoot: t.TempDir()}
	result := check.Run(ctx)
	if result.Status != StatusWarning {
		t.Fatalf("expected a zombie to be reported, got %v: %s", result.Status, result.Message)
	}
	if err := check.Fix(ctx); err != nil {
		t.Fatalf("Fix returned error: %v", err)
	}
	if !term.wasKilled("gt-morsov") {
		t.Errorf("genuine zombie was not killed (killed: %v)", term.killed)
	}
}

// TestOrphanFix_DoesNotKillUnparseableSession applies the same standard to the
// other destructive fix that shares the guard.
func TestOrphanFix_DoesNotKillUnparseableSession(t *testing.T) {
	setupTestRegistry(t)

	term := newFakeTerminator()
	check := NewOrphanSessionCheck()
	check.terminator = term
	// "pf-ghost" parses cleanly (pulseflow polecat) and is the disposable
	// control; the other two are the sessions the guard must refuse to kill.
	check.orphanSessions = []string{"hq-crew-joe", "gt-gastown-crew-joe", "pf-ghost"}

	if err := check.Fix(&CheckContext{TownRoot: t.TempDir()}); err != nil {
		t.Fatalf("Fix returned error: %v", err)
	}
	for _, protected := range []string{"hq-crew-joe", "gt-gastown-crew-joe"} {
		if term.wasKilled(protected) {
			t.Errorf("%q was killed by orphan cleanup (killed: %v)", protected, term.killed)
		}
	}
	if !term.wasKilled("pf-ghost") {
		t.Errorf("genuine orphan was not killed (killed: %v)", term.killed)
	}
}

func TestClassifySessionForKill(t *testing.T) {
	setupTestRegistry(t)

	tests := []struct {
		sess      string
		protected bool
		reason    string
	}{
		{"gt-crew-joe", true, "parses as crew"},
		{"nif-crew-codex1", true, "parses as crew"},
		{"gt-gastown-crew-joe", true, "long-form crew name that parses as a polecat"},
		{"hq-crew-joe", true, "unparseable"},
		{"hq-dog-", true, "unparseable: empty dog name"},
		{"", true, "unparseable"},
		{"gt-morsov", false, "ordinary polecat"},
		{"gt-witness", false, "witness"},
		{"gt-refinery", false, "refinery"},
		{"hq-mayor", false, "mayor"},
		{"hq-deacon", false, "deacon"},
	}

	for _, tt := range tests {
		t.Run(tt.sess, func(t *testing.T) {
			got := classifySessionForKill(tt.sess)
			if got.Protected != tt.protected {
				t.Errorf("classifySessionForKill(%q).Protected = %v, want %v (%s)", tt.sess, got.Protected, tt.protected, tt.reason)
			}
			if got.Protected && got.Reason == "" {
				t.Errorf("classifySessionForKill(%q) protected without a reason", tt.sess)
			}
		})
	}
}

// TestCrewProtection_DoesNotAdoptTheUnparseableRule pins the deliberate
// difference between the two entry points. classifySessionForKill protects
// names it cannot read; crewProtection does not, because the fixes that use it
// exist to remove sessions that are actively breaking the town and their
// hardest cases are exactly the names that will not resolve.
func TestCrewProtection_DoesNotAdoptTheUnparseableRule(t *testing.T) {
	setupTestRegistry(t)

	tests := []struct {
		sess          string
		crewProtected bool
		killProtected bool
	}{
		{"gt-crew-joe", true, true},         // crew either way
		{"gt-gastown-crew-joe", true, true}, // lexical crew segment
		{"hq-crew-joe", true, true},         // unparseable AND crew-looking
		{"zz-witness", false, true},         // unparseable, not crew-looking
		{"gt-morsov", false, false},         // ordinary polecat
	}

	for _, tt := range tests {
		t.Run(tt.sess, func(t *testing.T) {
			if got := crewProtection(tt.sess).Protected; got != tt.crewProtected {
				t.Errorf("crewProtection(%q).Protected = %v, want %v", tt.sess, got, tt.crewProtected)
			}
			if got := classifySessionForKill(tt.sess).Protected; got != tt.killProtected {
				t.Errorf("classifySessionForKill(%q).Protected = %v, want %v", tt.sess, got, tt.killProtected)
			}
		})
	}
}

package tmux

import (
	"errors"
	"testing"
	"time"
)

// fakeHealthProber stands in for tmux so the health levels can be exercised
// without a tmux server — in particular the probe-failure paths, which a real
// server will not produce on demand.
type fakeHealthProber struct {
	hasSession    bool
	hasSessionErr error
	agentAlive    bool
	agentAliveErr error
	activity      SessionActivity
	activityErr   error
}

func (f fakeHealthProber) HasSession(string) (bool, error) {
	return f.hasSession, f.hasSessionErr
}

func (f fakeHealthProber) IsAgentAliveChecked(string) (bool, error) {
	return f.agentAlive, f.agentAliveErr
}

func (f fakeHealthProber) GetSessionActivityDetail(string) (SessionActivity, error) {
	return f.activity, f.activityErr
}

// TestCheckSessionHealth_QueryFailureIsNotDeath is the gt-550 regression. A
// failed tmux query used to arrive as SessionDead, indistinguishable from a
// genuinely absent session — so one unreachable tmux server reported every
// agent in the town as dead, and callers that clear work or kill sessions on a
// death verdict would have acted on all of them at once.
func TestCheckSessionHealth_QueryFailureIsNotDeath(t *testing.T) {
	t.Parallel()

	probeErr := errors.New("tmux server unreachable")
	status, err := checkSessionHealth(fakeHealthProber{hasSessionErr: probeErr}, "gt-vault", 0)

	if status == SessionDead {
		t.Fatal("a failed session query was reported as SessionDead; unknown state must never be a death verdict")
	}
	if status != SessionUnknown {
		t.Errorf("status = %v, want SessionUnknown", status)
	}
	if !errors.Is(err, probeErr) {
		t.Errorf("err = %v, want it to wrap %v", err, probeErr)
	}
	if status.IsZombie() {
		t.Error("unknown liveness reported as a zombie; callers reap zombies")
	}
	if status.IsKnown() {
		t.Error("IsKnown() = true for a failed probe")
	}
}

// TestCheckSessionHealth_AgentProbeFailureIsNotDeath covers the second of the
// three conditions gt-550 names. Level 2 used to call IsAgentAlive, which
// discards its error, so a failed process query became AgentDead — the verdict
// callers act on by killing the session.
func TestCheckSessionHealth_AgentProbeFailureIsNotDeath(t *testing.T) {
	t.Parallel()

	probeErr := errors.New("ps failed")
	status, err := checkSessionHealth(fakeHealthProber{hasSession: true, agentAliveErr: probeErr}, "gt-vault", 0)

	if status == AgentDead {
		t.Fatal("a failed agent-process query was reported as AgentDead")
	}
	if status != SessionUnknown {
		t.Errorf("status = %v, want SessionUnknown", status)
	}
	if !errors.Is(err, probeErr) {
		t.Errorf("err = %v, want it to wrap %v", err, probeErr)
	}
}

func TestCheckSessionHealth_Levels(t *testing.T) {
	t.Parallel()

	created := time.Now().Add(-2 * time.Hour)
	// An attached session a human typed into an hour ago and has not touched
	// since. This is the strongest case the old code had for a hang, and it is
	// still not one: the field tracks keystrokes, not the agent (gt-0wz).
	stale := SessionActivity{Activity: created.Add(time.Hour), Created: created, Attached: true}
	fresh := SessionActivity{Activity: time.Now(), Created: created, Attached: true}
	tests := []struct {
		name          string
		prober        fakeHealthProber
		maxInactivity time.Duration
		want          ZombieStatus
		wantErr       bool
	}{
		{
			name:   "no such session is death, not uncertainty",
			prober: fakeHealthProber{hasSession: false},
			want:   SessionDead,
		},
		{
			name:   "session with a live agent is healthy",
			prober: fakeHealthProber{hasSession: true, agentAlive: true},
			want:   SessionHealthy,
		},
		{
			name:   "session without an agent process is agent-dead",
			prober: fakeHealthProber{hasSession: true},
			want:   AgentDead,
		},
		{
			name:          "stale activity cannot determine a hang",
			prober:        fakeHealthProber{hasSession: true, agentAlive: true, activity: stale},
			maxInactivity: time.Minute,
			want:          AgentHangUnknown,
		},
		{
			name:          "recent activity stays healthy",
			prober:        fakeHealthProber{hasSession: true, agentAlive: true, activity: fresh},
			maxInactivity: time.Minute,
			want:          SessionHealthy,
		},
		{
			// Level 3 has always refused to answer on error. Levels 1 and 2
			// now match it; this pins the behaviour that set the precedent.
			name:          "activity probe failure does not manufacture a verdict",
			prober:        fakeHealthProber{hasSession: true, agentAlive: true, activityErr: errors.New("no activity")},
			maxInactivity: time.Minute,
			want:          SessionHealthy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			status, err := checkSessionHealth(tt.prober, "gt-vault", tt.maxInactivity)
			if status != tt.want {
				t.Errorf("status = %v, want %v", status, tt.want)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestCheckSessionHealth_WrapperKeepsUnknownDistinct guards the convenience
// wrapper: discarding the error is allowed, inventing a death verdict is not.
func TestCheckSessionHealth_WrapperKeepsUnknownDistinct(t *testing.T) {
	t.Parallel()

	status, _ := checkSessionHealth(fakeHealthProber{hasSessionErr: errors.New("boom")}, "gt-vault", 0)
	if status == SessionDead || status == AgentDead || status == AgentHung {
		t.Errorf("status = %v; the error-discarding wrapper must still not report death", status)
	}
}

func TestSessionUnknownStatusLabel(t *testing.T) {
	t.Parallel()

	if got := SessionUnknown.String(); got != "liveness-unknown" {
		t.Errorf("SessionUnknown.String() = %q, want %q", got, "liveness-unknown")
	}
	for _, known := range []ZombieStatus{SessionHealthy, SessionDead, AgentDead, AgentHung} {
		if !known.IsKnown() {
			t.Errorf("%v.IsKnown() = false, want true", known)
		}
	}
}

// TestCheckSessionHealth_UnattachedAliveAgentIsNotHung is the gt-0wz regression,
// and it is the control that must fail before the fix: on the code this replaces
// it returned AgentHung.
//
// tmux updates #{session_activity} on CLIENT interaction, not on process output.
// Gas Town agents run unattached, so no client ever touches the field and it
// stays pinned at #{session_created} for the life of the session. Comparing it
// against a threshold therefore measures session AGE, and every agent that has
// been working steadily for longer than --max-inactivity was reported hung.
//
// Measured across the town while this was open: 21 of 22 sessions had
// activity == created, and the single session whose activity advanced was the
// only attached one. The verdict was tracking attachment, not health.
func TestCheckSessionHealth_UnattachedAliveAgentIsNotHung(t *testing.T) {
	t.Parallel()

	// A witness mid-cycle: created two days ago, demonstrably alive, and never
	// attached — so activity is still exactly created.
	created := time.Now().Add(-48 * time.Hour)
	prober := fakeHealthProber{
		hasSession: true,
		agentAlive: true,
		activity:   SessionActivity{Activity: created, Created: created, Attached: false},
	}

	status, err := checkSessionHealth(prober, "AVL-witness", 10*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status == AgentHung {
		t.Fatal("a live unattached agent was classified agent-hung; the verdict is reading session age, not activity")
	}
	if status.IsZombie() {
		t.Errorf("status = %v is a zombie verdict; callers kill the session and clear its work", status)
	}
	if status != AgentHangUnknown {
		t.Errorf("status = %v, want AgentHangUnknown — the check cannot determine liveness here and must say so", status)
	}
}

// TestCheckSessionHealth_AttachedStaleAgentIsNotHung is the other half of gt-0wz,
// and the reason gating on Advanced() alone is not enough.
//
// The town's one attached session had an activity timestamp seven hours old while
// the agent was actively running commands — because the timestamp records when a
// human last typed, not what the agent is doing. Probed directly: 400 lines of
// pane output moved #{session_activity} in neither an attached nor an unattached
// session, and one client keystroke moved it at once.
//
// So "the field advanced at some point" does not license a hang verdict either.
// If an attached session were the only shape that could still be condemned, the
// check would be measuring attachment rather than health.
func TestCheckSessionHealth_AttachedStaleAgentIsNotHung(t *testing.T) {
	t.Parallel()

	created := time.Now().Add(-48 * time.Hour)
	prober := fakeHealthProber{
		hasSession: true,
		agentAlive: true,
		// A human typed 47 hours ago; the agent has worked ever since.
		activity: SessionActivity{Activity: created.Add(time.Hour), Created: created, Attached: true},
	}

	status, err := checkSessionHealth(prober, "hq-mayor", 10*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status == AgentHung {
		t.Fatal("an attached session was classified agent-hung from a keystroke timestamp")
	}
	if status.IsZombie() {
		t.Errorf("status = %v is a zombie verdict; callers reap zombies", status)
	}
	if status != AgentHangUnknown {
		t.Errorf("status = %v, want AgentHangUnknown", status)
	}
}

// TestCheckSessionHealth_ActivityNeverYieldsHang states the rule directly, so a
// later change that reintroduces a hang verdict from this field has to delete an
// explicit assertion rather than quietly slipping past a table case. Every
// combination of attached and advanced must land on "cannot determine".
func TestCheckSessionHealth_ActivityNeverYieldsHang(t *testing.T) {
	t.Parallel()

	created := time.Now().Add(-48 * time.Hour)
	for _, tc := range []struct {
		name string
		act  SessionActivity
	}{
		{"unattached, never typed into", SessionActivity{Activity: created, Created: created}},
		{"unattached, typed into long ago", SessionActivity{Activity: created.Add(time.Hour), Created: created}},
		{"attached, never typed into", SessionActivity{Activity: created, Created: created, Attached: true}},
		{"attached, typed into long ago", SessionActivity{Activity: created.Add(time.Hour), Created: created, Attached: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, _ := checkSessionHealth(
				fakeHealthProber{hasSession: true, agentAlive: true, activity: tc.act},
				"gt-agent", 10*time.Minute)
			if status == AgentHung {
				t.Errorf("status = AgentHung; #{session_activity} must never produce a hang verdict")
			}
		})
	}
}

// TestSessionActivity_Stale pins the timestamp predicate. A zero value is not
// stale: it means the field was never read, and level 3 declines to answer rather
// than manufacturing a verdict from it.
func TestSessionActivity_Stale(t *testing.T) {
	t.Parallel()

	if (SessionActivity{}).Stale(time.Minute) {
		t.Error("a zero activity time reported stale")
	}
	if !(SessionActivity{Activity: time.Now().Add(-time.Hour)}).Stale(time.Minute) {
		t.Error("an hour-old activity time did not report stale against a 1m threshold")
	}
	if (SessionActivity{Activity: time.Now()}).Stale(time.Minute) {
		t.Error("a just-now activity time reported stale")
	}
}

// TestSessionActivity_Advanced pins the discriminator itself. A field equal to
// session_created has never moved; only a later value proves tmux is tracking
// this session.
func TestSessionActivity_Advanced(t *testing.T) {
	t.Parallel()

	created := time.Now().Add(-time.Hour)
	tests := []struct {
		name string
		act  SessionActivity
		want bool
	}{
		{"frozen at creation", SessionActivity{Activity: created, Created: created}, false},
		{"advanced past creation", SessionActivity{Activity: created.Add(time.Minute), Created: created}, true},
		{"before creation is not advancement", SessionActivity{Activity: created.Add(-time.Minute), Created: created}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.act.Advanced(); got != tt.want {
				t.Errorf("Advanced() = %v, want %v", got, tt.want)
			}
		})
	}
}

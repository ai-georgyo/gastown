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
	activity      time.Time
	activityErr   error
}

func (f fakeHealthProber) HasSession(string) (bool, error) {
	return f.hasSession, f.hasSessionErr
}

func (f fakeHealthProber) IsAgentAliveChecked(string) (bool, error) {
	return f.agentAlive, f.agentAliveErr
}

func (f fakeHealthProber) GetSessionActivity(string) (time.Time, error) {
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

	stale := time.Now().Add(-time.Hour)
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
			name:          "stale activity is agent-hung",
			prober:        fakeHealthProber{hasSession: true, agentAlive: true, activity: stale},
			maxInactivity: time.Minute,
			want:          AgentHung,
		},
		{
			name:          "recent activity stays healthy",
			prober:        fakeHealthProber{hasSession: true, agentAlive: true, activity: time.Now()},
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

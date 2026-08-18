package dog

import (
	"fmt"
	"time"

	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/tmux"
)

// sessionChecker abstracts the tmux health-check methods needed by the
// health checker.  Satisfied by *tmux.Tmux; mockable in tests.
type sessionChecker interface {
	CheckSessionHealth(session string, maxInactivity time.Duration) tmux.ZombieStatus
	GetSessionActivityDetail(session string) (tmux.SessionActivity, error)
	HasSession(name string) (bool, error)
	KillSession(name string) error
}

// heartbeatReader reads an agent's self-reported heartbeat. Split out so the
// override can be tested without a filesystem.
type heartbeatReader func(sessionName string) *polecat.SessionHeartbeat

// LivenessDetail records the raw signals behind a session verdict so a reader can
// see why it was reached instead of inferring it (gt-0wz).
//
// The three timestamps are reported together deliberately. #{session_activity}
// tracks client keystrokes, so for the unattached sessions agents run in it never
// moves at all; printing it beside Created and Now makes that visible —
// Activity == Created on a session hours old is the signature.
type LivenessDetail struct {
	// Activity is tmux #{session_activity}.
	Activity time.Time `json:"activity"`
	// Created is tmux #{session_created}.
	Created time.Time `json:"created"`
	// Now is when the check ran.
	Now time.Time `json:"now"`
	// Attached reports whether a client is attached. If the check's verdicts
	// split on this field, it is measuring attachment rather than health.
	Attached bool `json:"attached"`
	// ActivityAdvanced reports whether Activity ever moved past Created, i.e.
	// whether a client has ever typed into this session. When false, Activity's
	// age is the session's age; when true it is the time since a human typed.
	// Neither is the agent's idle time.
	ActivityAdvanced bool `json:"activity_advanced"`
	// ActivityError is set when the tmux fields could not be read.
	ActivityError string `json:"activity_error,omitempty"`
	// HeartbeatAge is how long ago the agent last wrote a heartbeat. Empty when
	// no heartbeat file exists.
	HeartbeatAge string `json:"heartbeat_age,omitempty"`
	// HeartbeatState is the agent's self-reported state (heartbeat v2, gt-3vr5).
	HeartbeatState string `json:"heartbeat_state,omitempty"`
}

// DogHealthResult describes the health of a single dog.
type DogHealthResult struct {
	Name           string        `json:"name"`
	State          State         `json:"state"`
	SessionStatus  string        `json:"session_status"`          // from ZombieStatus.String()
	WorkDuration   time.Duration `json:"work_duration,omitempty"` // how long current work has been running
	NeedsAttention bool          `json:"needs_attention"`
	AutoCleared    bool          `json:"auto_cleared,omitempty"`
	Recommendation string        `json:"recommendation,omitempty"`
	// Liveness carries the raw signals behind SessionStatus. Populated whenever
	// an inactivity threshold was in play, which is when they can mislead.
	Liveness *LivenessDetail `json:"liveness,omitempty"`
}

// HealthChecker performs health checks on dogs in the kennel.
type HealthChecker struct {
	mgr       *Manager
	checker   sessionChecker
	heartbeat heartbeatReader
}

// NewHealthChecker creates a HealthChecker.
func NewHealthChecker(mgr *Manager, checker sessionChecker) *HealthChecker {
	townRoot := mgr.TownRoot()
	return &HealthChecker{
		mgr:     mgr,
		checker: checker,
		heartbeat: func(sessionName string) *polecat.SessionHeartbeat {
			return polecat.ReadSessionHeartbeat(townRoot, sessionName)
		},
	}
}

// dogSessionName returns the tmux session name for a dog.
func dogSessionName(name string) string {
	return fmt.Sprintf("hq-dog-%s", name)
}

// Check performs a health check on a single dog.
func (hc *HealthChecker) Check(d *Dog, maxInactivity time.Duration, autoClear bool) DogHealthResult {
	result := DogHealthResult{
		Name:  d.Name,
		State: d.State,
	}

	// Compute work duration if working and WorkStartedAt is set.
	if d.State == StateWorking && !d.WorkStartedAt.IsZero() {
		result.WorkDuration = time.Since(d.WorkStartedAt)
	}

	session := dogSessionName(d.Name)

	switch d.State {
	case StateWorking:
		status := hc.checker.CheckSessionHealth(session, maxInactivity)

		// Record the raw signals and, where a hang was suspected, give the agent
		// a chance to speak for itself before the verdict stands (gt-0wz).
		// The heartbeat is read once and shared, so the reported detail and the
		// verdict cannot disagree about it.
		if maxInactivity > 0 {
			var hb *polecat.SessionHeartbeat
			if hc.heartbeat != nil {
				hb = hc.heartbeat(session)
			}
			result.Liveness = hc.livenessDetail(session, hb)
			status = hc.reconsiderHang(status, hb, &result)
		}

		result.SessionStatus = status.String()

		switch status {
		case tmux.SessionDead:
			// Zombie: state says working but session is gone.
			result.NeedsAttention = true
			result.Recommendation = "zombie: session dead but state=working"
			if autoClear {
				if err := hc.mgr.ClearWork(d.Name); err == nil {
					result.AutoCleared = true
					result.Recommendation = "zombie auto-cleared (session dead)"
				}
			}

		case tmux.AgentDead:
			// Zombie: session exists but agent process died.
			result.NeedsAttention = true
			result.Recommendation = "zombie: agent dead in session"
			if autoClear {
				_ = hc.checker.KillSession(session)
				if err := hc.mgr.ClearWork(d.Name); err == nil {
					result.AutoCleared = true
					result.Recommendation = "zombie auto-cleared (agent dead, session killed)"
				}
			}

		case tmux.AgentHung:
			// Hung: process alive but no tmux activity for maxInactivity.
			// If autoClear is on, kill and reclaim — the dog almost certainly
			// finished its work but failed to call `gt dog done`.
			result.NeedsAttention = true
			if autoClear {
				_ = hc.checker.KillSession(session)
				if err := hc.mgr.ClearWork(d.Name); err == nil {
					result.AutoCleared = true
					result.Recommendation = "hung dog auto-cleared (idle prompt, session killed)"
				} else {
					result.Recommendation = "hung: auto-clear failed: " + err.Error()
				}
			} else {
				result.Recommendation = "hung: agent alive but no tmux activity"
			}

		case tmux.AgentHangUnknown:
			// Session and agent process are both confirmed alive; only idleness
			// was undeterminable, because tmux #{session_activity} tracks client
			// keystrokes rather than agent activity. That is not evidence of a
			// hang, and auto-clearing on it killed steadily-working dogs
			// (gt-0wz). Report it without demanding attention or reaping.
			//
			// A dog that really has stopped shows up as a stale heartbeat here,
			// which reconsiderHang deliberately does not promote to a hang
			// either: a heartbeat advances on gt invocations, so a long non-gt
			// step looks identical. Until a signal that follows agent activity
			// exists, "cannot determine" is the honest answer, and it is the
			// safe one — the failure mode of guessing was killing live agents.
			result.Recommendation = "hang undetermined: agent process alive, tmux activity tracks client keystrokes not agent work - not clearing work"

		case tmux.SessionUnknown:
			// A probe failed, so nothing was observed. Flag it, but do not
			// auto-clear: clearing work on an unknown verdict is how a single
			// unreachable tmux server would reclaim every working dog (gt-550).
			result.NeedsAttention = true
			result.Recommendation = "liveness unknown: tmux probe failed - not clearing work"

		default: // SessionHealthy — status.String() already set above
		}

	case StateIdle:
		// Check for orphan session.
		has, _ := hc.checker.HasSession(session)
		if has {
			result.SessionStatus = "orphan"
			result.NeedsAttention = true
			if autoClear {
				_ = hc.checker.KillSession(session)
				result.AutoCleared = true
				result.Recommendation = "orphan auto-cleared (session killed)"
			} else {
				result.Recommendation = "orphan: dog idle but tmux session exists"
			}
		} else {
			result.SessionStatus = "none"
		}
	}

	return result
}

// livenessDetail reads the signals behind a staleness verdict. It never fails:
// an unreadable probe is reported in the struct rather than dropped, because the
// point of these fields is to make a broken signal visible.
func (hc *HealthChecker) livenessDetail(session string, hb *polecat.SessionHeartbeat) *LivenessDetail {
	d := &LivenessDetail{Now: time.Now()}

	act, err := hc.checker.GetSessionActivityDetail(session)
	if err != nil {
		d.ActivityError = err.Error()
	} else {
		d.Activity = act.Activity
		d.Created = act.Created
		d.Attached = act.Attached
		d.ActivityAdvanced = act.Advanced()
	}

	if hb != nil {
		d.HeartbeatAge = time.Since(hb.Timestamp).Truncate(time.Second).String()
		d.HeartbeatState = string(hb.EffectiveState())
	}

	return d
}

// reconsiderHang upgrades an undetermined or hung verdict to healthy when the
// agent has written a fresh heartbeat.
//
// A heartbeat is a signal the agent produces itself, so unlike
// #{session_activity} it does not depend on a client typing. The witness already
// prefers it over activity scraping for the same reason (gt-3vr5), and dogs write
// one on every gt invocation.
//
// It only ever overrides toward alive. A stale heartbeat is not treated as proof
// of a hang, because a heartbeat advances on gt invocations rather than
// continuously, and a long non-gt step is normal.
func (hc *HealthChecker) reconsiderHang(status tmux.ZombieStatus, hb *polecat.SessionHeartbeat, result *DogHealthResult) tmux.ZombieStatus {
	if status != tmux.AgentHung && status != tmux.AgentHangUnknown {
		return status
	}
	if hb == nil || time.Since(hb.Timestamp) >= polecat.SessionHeartbeatStaleThreshold {
		return status
	}

	// The agent reported in recently, so it is alive whatever tmux thinks.
	age := time.Since(hb.Timestamp).Truncate(time.Second).String()

	if hb.EffectiveState() == polecat.HeartbeatStuck {
		// A self-report is a real signal and worth surfacing, but it is not a
		// hang inferred from a timestamp — and it must not enter the reaping
		// path, which kills the session out from under a live agent. Flag it and
		// let the Deacon decide, per ZFC.
		result.NeedsAttention = true
		result.Recommendation = fmt.Sprintf("agent self-reports stuck (heartbeat %s) - alive, not reaped", age)
		return tmux.SessionHealthy
	}

	result.Recommendation = fmt.Sprintf("fresh heartbeat (%s, state=%s) overrides stale tmux activity", age, hb.EffectiveState())
	return tmux.SessionHealthy
}

// CheckAll performs health checks on all dogs.
func (hc *HealthChecker) CheckAll(maxInactivity time.Duration, autoClear bool) ([]DogHealthResult, error) {
	dogs, err := hc.mgr.List()
	if err != nil {
		return nil, fmt.Errorf("listing dogs: %w", err)
	}

	results := make([]DogHealthResult, 0, len(dogs))
	for _, d := range dogs {
		results = append(results, hc.Check(d, maxInactivity, autoClear))
	}
	return results, nil
}

// NeedsAttentionCount returns how many results need attention.
func NeedsAttentionCount(results []DogHealthResult) int {
	n := 0
	for _, r := range results {
		if r.NeedsAttention {
			n++
		}
	}
	return n
}

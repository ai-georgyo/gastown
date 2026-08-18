package dog

import (
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/tmux"
)

// mockSessionChecker implements sessionChecker for testing.
type mockSessionChecker struct {
	healthResults  map[string]tmux.ZombieStatus    // session -> status
	activity       map[string]tmux.SessionActivity // session -> tmux activity fields
	activityErr    error
	sessionsAlive  map[string]bool // session -> exists
	killedSessions []string
}

func newMockChecker() *mockSessionChecker {
	return &mockSessionChecker{
		healthResults: make(map[string]tmux.ZombieStatus),
		activity:      make(map[string]tmux.SessionActivity),
		sessionsAlive: make(map[string]bool),
	}
}

func (m *mockSessionChecker) CheckSessionHealth(session string, _ time.Duration) tmux.ZombieStatus {
	if s, ok := m.healthResults[session]; ok {
		return s
	}
	return tmux.SessionDead
}

func (m *mockSessionChecker) GetSessionActivityDetail(session string) (tmux.SessionActivity, error) {
	if m.activityErr != nil {
		return tmux.SessionActivity{}, m.activityErr
	}
	return m.activity[session], nil
}

func (m *mockSessionChecker) HasSession(name string) (bool, error) {
	return m.sessionsAlive[name], nil
}

func (m *mockSessionChecker) KillSession(name string) error {
	m.killedSessions = append(m.killedSessions, name)
	return nil
}

// =============================================================================
// Healthy dogs
// =============================================================================

func TestHealth_IdleDog_NoSession(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateIdle, LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	hc := NewHealthChecker(m, mc)

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, false)

	if r.NeedsAttention {
		t.Error("idle dog with no session should not need attention")
	}
	if r.SessionStatus != "none" {
		t.Errorf("session_status = %q, want 'none'", r.SessionStatus)
	}
	if r.WorkDuration != 0 {
		t.Errorf("work_duration = %v, want 0", r.WorkDuration)
	}
}

func TestHealth_WorkingDog_Healthy(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	workStart := now.Add(-10 * time.Minute)
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateWorking, Work: "task-1",
		WorkStartedAt: workStart, LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.healthResults["hq-dog-alpha"] = tmux.SessionHealthy
	hc := NewHealthChecker(m, mc)

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, false)

	if r.NeedsAttention {
		t.Error("healthy working dog should not need attention")
	}
	if r.SessionStatus != "healthy" {
		t.Errorf("session_status = %q, want 'healthy'", r.SessionStatus)
	}
	if r.WorkDuration < 10*time.Minute {
		t.Errorf("work_duration = %v, want >= 10m", r.WorkDuration)
	}
}

// =============================================================================
// Zombies
// =============================================================================

func TestHealth_Zombie_SessionDead(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateWorking, Work: "task-1",
		WorkStartedAt: now.Add(-1 * time.Hour), LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.healthResults["hq-dog-alpha"] = tmux.SessionDead
	hc := NewHealthChecker(m, mc)

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, false)

	if !r.NeedsAttention {
		t.Error("zombie (SessionDead) should need attention")
	}
	if r.AutoCleared {
		t.Error("should not auto-clear when autoClear=false")
	}
}

func TestHealth_Zombie_AgentDead(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateWorking, Work: "task-1",
		WorkStartedAt: now.Add(-1 * time.Hour), LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.healthResults["hq-dog-alpha"] = tmux.AgentDead
	hc := NewHealthChecker(m, mc)

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, false)

	if !r.NeedsAttention {
		t.Error("zombie (AgentDead) should need attention")
	}
	if r.AutoCleared {
		t.Error("should not auto-clear when autoClear=false")
	}
}

// =============================================================================
// Hung
// =============================================================================

func TestHealth_Hung_ReportOnly(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateWorking, Work: "task-1",
		WorkStartedAt: now.Add(-2 * time.Hour), LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.healthResults["hq-dog-alpha"] = tmux.AgentHung
	hc := NewHealthChecker(m, mc)

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, false) // autoClear=false: report only

	if !r.NeedsAttention {
		t.Error("hung dog should need attention")
	}
	if r.AutoCleared {
		t.Error("hung dog should NOT be auto-cleared when autoClear=false")
	}
	if r.SessionStatus != "agent-hung" {
		t.Errorf("session_status = %q, want 'agent-hung'", r.SessionStatus)
	}
}

func TestHealth_Hung_AutoCleared(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateWorking, Work: "task-1",
		WorkStartedAt: now.Add(-2 * time.Hour), LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.healthResults["hq-dog-alpha"] = tmux.AgentHung
	hc := NewHealthChecker(m, mc)

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, true) // autoClear=true: kill and reclaim

	if !r.NeedsAttention {
		t.Error("hung dog should need attention")
	}
	if !r.AutoCleared {
		t.Error("hung dog should be auto-cleared when autoClear=true")
	}
	if len(mc.killedSessions) != 1 || mc.killedSessions[0] != "hq-dog-alpha" {
		t.Errorf("killedSessions = %v, want [hq-dog-alpha]", mc.killedSessions)
	}

	// Verify state was cleared
	d2, _ := m.Get("alpha")
	if d2.State != StateIdle {
		t.Errorf("state = %q, want idle after auto-clear", d2.State)
	}
}

// =============================================================================
// Auto-clear zombies
// =============================================================================

func TestHealth_AutoClear_SessionDead(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateWorking, Work: "task-1",
		WorkStartedAt: now.Add(-1 * time.Hour), LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.healthResults["hq-dog-alpha"] = tmux.SessionDead
	hc := NewHealthChecker(m, mc)

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, true)

	if !r.AutoCleared {
		t.Error("zombie (SessionDead) should be auto-cleared")
	}

	// Verify state was actually cleared
	d2, _ := m.Get("alpha")
	if d2.State != StateIdle {
		t.Errorf("state = %q, want idle after auto-clear", d2.State)
	}
	if d2.Work != "" {
		t.Errorf("work = %q, want empty after auto-clear", d2.Work)
	}
}

func TestHealth_AutoClear_AgentDead(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateWorking, Work: "task-1",
		WorkStartedAt: now.Add(-1 * time.Hour), LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.healthResults["hq-dog-alpha"] = tmux.AgentDead
	hc := NewHealthChecker(m, mc)

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, true)

	if !r.AutoCleared {
		t.Error("zombie (AgentDead) should be auto-cleared")
	}

	// Verify session was killed
	if len(mc.killedSessions) != 1 || mc.killedSessions[0] != "hq-dog-alpha" {
		t.Errorf("killedSessions = %v, want [hq-dog-alpha]", mc.killedSessions)
	}

	// Verify state was cleared
	d2, _ := m.Get("alpha")
	if d2.State != StateIdle {
		t.Errorf("state = %q, want idle after auto-clear", d2.State)
	}
}

// TestHealth_LivenessUnknown_NeverAutoClears is the gt-550 consequence measured
// here rather than argued. Before the fix, a failed tmux query returned
// SessionDead, so this exact path auto-cleared a working dog's work and killed
// nothing it had verified was gone — and it would have done it to every dog at
// once, because the failure is server-wide.
func TestHealth_LivenessUnknown_NeverAutoClears(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateWorking, Work: "task-1",
		WorkStartedAt: now.Add(-1 * time.Hour), LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.healthResults["hq-dog-alpha"] = tmux.SessionUnknown
	hc := NewHealthChecker(m, mc)

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, true) // autoClear ON — the dangerous setting

	if r.AutoCleared {
		t.Error("work was auto-cleared on an unknown liveness verdict")
	}
	if len(mc.killedSessions) != 0 {
		t.Errorf("killedSessions = %v, want none on an unknown verdict", mc.killedSessions)
	}
	if !r.NeedsAttention {
		t.Error("unknown liveness should be surfaced, not silently treated as healthy")
	}
	if r.SessionStatus != "liveness-unknown" {
		t.Errorf("session_status = %q, want 'liveness-unknown'", r.SessionStatus)
	}

	d2, _ := m.Get("alpha")
	if d2.State != StateWorking || d2.Work != "task-1" {
		t.Errorf("dog = state %q work %q; work must survive an unknown verdict", d2.State, d2.Work)
	}
}

// =============================================================================
// Orphan sessions
// =============================================================================

func TestHealth_Orphan_IdleWithSession(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateIdle, LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.sessionsAlive["hq-dog-alpha"] = true
	hc := NewHealthChecker(m, mc)

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, false)

	if !r.NeedsAttention {
		t.Error("orphan session should need attention")
	}
	if r.SessionStatus != "orphan" {
		t.Errorf("session_status = %q, want 'orphan'", r.SessionStatus)
	}
}

func TestHealth_Orphan_AutoCleared(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateIdle, LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.sessionsAlive["hq-dog-alpha"] = true
	hc := NewHealthChecker(m, mc)

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, true) // autoClear=true: kill orphan session

	if !r.NeedsAttention {
		t.Error("orphan session should need attention")
	}
	if !r.AutoCleared {
		t.Error("orphan session should be auto-cleared when autoClear=true")
	}
	if len(mc.killedSessions) != 1 || mc.killedSessions[0] != "hq-dog-alpha" {
		t.Errorf("killedSessions = %v, want [hq-dog-alpha]", mc.killedSessions)
	}
}

// =============================================================================
// WorkDuration computation
// =============================================================================

func TestHealth_WorkDuration_ZeroStartedAt(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	// Working dog with zero WorkStartedAt (legacy state file)
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateWorking, Work: "task-1",
		LastActive: now, CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.healthResults["hq-dog-alpha"] = tmux.SessionHealthy
	hc := NewHealthChecker(m, mc)

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, false)

	if r.WorkDuration != 0 {
		t.Errorf("work_duration = %v, want 0 for zero WorkStartedAt", r.WorkDuration)
	}
}

// =============================================================================
// CheckAll
// =============================================================================

func TestHealth_CheckAll_MultipleDogs(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()

	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateIdle, LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})
	setupDogWithState(t, m, "beta", &DogState{
		Name: "beta", State: StateWorking, Work: "task-1",
		WorkStartedAt: now.Add(-1 * time.Hour), LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.healthResults["hq-dog-beta"] = tmux.SessionDead // zombie
	hc := NewHealthChecker(m, mc)

	results, err := hc.CheckAll(30*time.Minute, false)
	if err != nil {
		t.Fatalf("CheckAll() error = %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("CheckAll() returned %d results, want 2", len(results))
	}

	attention := NeedsAttentionCount(results)
	if attention != 1 {
		t.Errorf("NeedsAttentionCount = %d, want 1", attention)
	}
}

// =============================================================================
// NeedsAttentionCount
// =============================================================================

func TestNeedsAttentionCount(t *testing.T) {
	results := []DogHealthResult{
		{Name: "a", NeedsAttention: false},
		{Name: "b", NeedsAttention: true},
		{Name: "c", NeedsAttention: true},
		{Name: "d", NeedsAttention: false},
	}

	if got := NeedsAttentionCount(results); got != 2 {
		t.Errorf("NeedsAttentionCount = %d, want 2", got)
	}

	if got := NeedsAttentionCount(nil); got != 0 {
		t.Errorf("NeedsAttentionCount(nil) = %d, want 0", got)
	}
}

// =============================================================================
// gt-0wz: session_activity is not a liveness signal for unattached sessions
// =============================================================================

// TestHealth_HangUnknown_IsNotReaped is the destructive half of gt-0wz. tmux only
// advances #{session_activity} on client interaction, so for the unattached
// sessions agents actually run in the field stays pinned at session creation and
// every dog working longer than --max-inactivity was reported hung. With
// --auto-clear that verdict kills the session and clears the work, so a single
// health-check run would have reaped the whole kennel.
func TestHealth_HangUnknown_IsNotReaped(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateWorking, Work: "task-1",
		WorkStartedAt: now.Add(-2 * time.Hour), LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.healthResults["hq-dog-alpha"] = tmux.AgentHangUnknown
	hc := NewHealthChecker(m, mc)

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, true) // autoClear on: the dangerous path

	if r.AutoCleared {
		t.Error("work was cleared on an undetermined hang; the agent is known alive")
	}
	if len(mc.killedSessions) != 0 {
		t.Errorf("killedSessions = %v, want none — killing a live agent's session", mc.killedSessions)
	}
	if r.NeedsAttention {
		t.Error("an undetermined hang demanded attention; every unattached agent trips this")
	}
	if r.SessionStatus != "hang-unknown" {
		t.Errorf("session_status = %q, want 'hang-unknown'", r.SessionStatus)
	}
}

// TestHealth_FreshHeartbeatOverridesStaleActivity covers the other shape the tmux
// layer cannot fix on its own: a session whose activity field does advance but
// lags far behind the work the agent is doing — the state the mayor's session was
// in, 417 minutes "stale" while actively running commands.
//
// A heartbeat is written by the agent itself, so it does not depend on a client
// being attached. The witness already prefers it over activity scraping (gt-3vr5).
func TestHealth_FreshHeartbeatOverridesStaleActivity(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateWorking, Work: "task-1",
		WorkStartedAt: now.Add(-2 * time.Hour), LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.healthResults["hq-dog-alpha"] = tmux.AgentHung
	hc := NewHealthChecker(m, mc)
	hc.heartbeat = func(string) *polecat.SessionHeartbeat {
		return &polecat.SessionHeartbeat{Timestamp: now, State: polecat.HeartbeatWorking}
	}

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, true)

	if r.SessionStatus != "healthy" {
		t.Errorf("session_status = %q, want 'healthy' — the agent reported in seconds ago", r.SessionStatus)
	}
	if r.AutoCleared || len(mc.killedSessions) != 0 {
		t.Error("a dog with a fresh heartbeat was reaped")
	}
}

// TestHealth_StaleHeartbeatLeavesHangVerdictStanding guards against the override
// swallowing real hangs. A heartbeat older than the stale threshold is no
// evidence of life, so the tmux verdict must stand.
func TestHealth_StaleHeartbeatLeavesHangVerdictStanding(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateWorking, Work: "task-1",
		WorkStartedAt: now.Add(-2 * time.Hour), LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.healthResults["hq-dog-alpha"] = tmux.AgentHung
	hc := NewHealthChecker(m, mc)
	hc.heartbeat = func(string) *polecat.SessionHeartbeat {
		return &polecat.SessionHeartbeat{
			Timestamp: now.Add(-2 * polecat.SessionHeartbeatStaleThreshold),
			State:     polecat.HeartbeatWorking,
		}
	}

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, false)

	if r.SessionStatus != "agent-hung" {
		t.Errorf("session_status = %q, want 'agent-hung'", r.SessionStatus)
	}
	if !r.NeedsAttention {
		t.Error("a genuine hang stopped needing attention")
	}
}

// TestHealth_SelfReportedStuckIsFlaggedNotReaped: an agent that says it is stuck
// is still running, so killing its session destroys live work. Flag it and let
// the Deacon decide, per ZFC.
func TestHealth_SelfReportedStuckIsFlaggedNotReaped(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateWorking, Work: "task-1",
		WorkStartedAt: now.Add(-2 * time.Hour), LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.healthResults["hq-dog-alpha"] = tmux.AgentHung
	hc := NewHealthChecker(m, mc)
	hc.heartbeat = func(string) *polecat.SessionHeartbeat {
		return &polecat.SessionHeartbeat{Timestamp: now, State: polecat.HeartbeatStuck}
	}

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, true)

	if !r.NeedsAttention {
		t.Error("a self-reported stuck dog should need attention")
	}
	if r.AutoCleared || len(mc.killedSessions) != 0 {
		t.Error("a live agent's session was killed on its own stuck report")
	}
}

// TestHealth_LivenessDetailIsReported covers the diagnostic requirement: the raw
// signals must be printed beside the verdict so a future freeze is visible rather
// than inferred, including whether a client was attached.
func TestHealth_LivenessDetailIsReported(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateWorking, Work: "task-1",
		WorkStartedAt: now.Add(-2 * time.Hour), LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	created := now.Add(-48 * time.Hour)
	mc := newMockChecker()
	mc.healthResults["hq-dog-alpha"] = tmux.AgentHangUnknown
	mc.activity["hq-dog-alpha"] = tmux.SessionActivity{
		Activity: created, Created: created, Attached: false,
	}
	hc := NewHealthChecker(m, mc)

	d, _ := m.Get("alpha")
	r := hc.Check(d, 30*time.Minute, false)

	if r.Liveness == nil {
		t.Fatal("Liveness detail missing; a frozen activity field would be invisible")
	}
	if !r.Liveness.Activity.Equal(created) || !r.Liveness.Created.Equal(created) {
		t.Errorf("Liveness activity/created = %v/%v, want both %v",
			r.Liveness.Activity, r.Liveness.Created, created)
	}
	if r.Liveness.ActivityAdvanced {
		t.Error("ActivityAdvanced = true for a field pinned at session creation")
	}
	if r.Liveness.Attached {
		t.Error("Attached = true for an unattached session")
	}
	if r.Liveness.Now.IsZero() {
		t.Error("Now is zero; the reader cannot tell how old the timestamps are")
	}
}

// TestHealth_NoLivenessDetailWithoutThreshold: callers that pass 0 skip the
// staleness level entirely, so there is nothing to explain.
func TestHealth_NoLivenessDetailWithoutThreshold(t *testing.T) {
	m, _ := testManager(t)
	now := time.Now()
	setupDogWithState(t, m, "alpha", &DogState{
		Name: "alpha", State: StateWorking, Work: "task-1",
		WorkStartedAt: now.Add(-2 * time.Hour), LastActive: now,
		CreatedAt: now, UpdatedAt: now,
	})

	mc := newMockChecker()
	mc.healthResults["hq-dog-alpha"] = tmux.SessionHealthy
	hc := NewHealthChecker(m, mc)

	d, _ := m.Get("alpha")
	if r := hc.Check(d, 0, false); r.Liveness != nil {
		t.Error("Liveness populated when no inactivity threshold was in play")
	}
}

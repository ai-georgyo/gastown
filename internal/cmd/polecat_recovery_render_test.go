package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/lock"
	"github.com/steveyegge/gastown/internal/polecat"
)

// claimsSafe reports whether rendered output would tell a reader (human or
// Witness following the cleanup checklist) that this sandbox can be destroyed.
func claimsSafe(out string) bool {
	return strings.Contains(out, "SAFE_TO_NUKE") || strings.Contains(strings.ToLower(out), "safe to nuke")
}

// TestRenderRecoveryStatus_WorkingPolecatIsNeverRenderedSafe is the gt-85p
// regression guard. The decision engine already returned WORKING for a live
// polecat; the human renderer's `default:` arm printed "SAFE_TO_NUKE / Safe to
// nuke - no work at risk" anyway, and that text is what the Witness cleanup
// checklist reads before destroying a sandbox.
func TestRenderRecoveryStatus_WorkingPolecatIsNeverRenderedSafe(t *testing.T) {
	status := &RecoveryStatus{
		Rig:                  "gastown",
		Polecat:              "rictus",
		Verdict:              polecat.WorkstateVerdictWorking,
		Reason:               "not-idle",
		SafeToNuke:           false,
		CountsTowardCapacity: true,
		Branch:               "polecat/rictus/gt-85p+msucbz4m",
		Issue:                "gt-85p",
	}

	var buf bytes.Buffer
	renderRecoveryStatus(&buf, "gastown", "rictus", status)
	out := buf.String()

	if claimsSafe(out) {
		t.Errorf("WORKING polecat rendered as safe to nuke:\n%s", out)
	}
	if !strings.Contains(out, polecat.WorkstateVerdictWorking) {
		t.Errorf("output does not report the WORKING verdict:\n%s", out)
	}
	if !strings.Contains(out, "Do NOT nuke") {
		t.Errorf("output does not warn against nuking:\n%s", out)
	}
}

// TestRenderRecoveryStatus_NeverClaimsSafeWithoutSafeToNuke checks every
// non-safe verdict, including verdicts this renderer has never seen. An
// unrecognized verdict must fail safe rather than fall through to the safe arm.
func TestRenderRecoveryStatus_NeverClaimsSafeWithoutSafeToNuke(t *testing.T) {
	verdicts := []string{
		polecat.WorkstateVerdictWorking,
		polecat.WorkstateVerdictNeedsRecovery,
		polecat.WorkstateVerdictPendingMR,
		polecat.WorkstateVerdictNeedsMQSubmit,
		"STALLED",
		"SOME_FUTURE_VERDICT",
		"",
	}

	for _, verdict := range verdicts {
		t.Run("verdict="+verdict, func(t *testing.T) {
			var buf bytes.Buffer
			renderRecoveryStatus(&buf, "gastown", "rictus", &RecoveryStatus{
				Verdict:    verdict,
				SafeToNuke: false,
				Branch:     "polecat/rictus/gt-85p+msucbz4m",
			})
			if claimsSafe(buf.String()) {
				t.Errorf("verdict %q rendered as safe to nuke:\n%s", verdict, buf.String())
			}
		})
	}
}

// TestRenderRecoveryStatus_SafeVerdictStillRendersSafe guards the other
// direction: the fix must not make cleanup impossible.
func TestRenderRecoveryStatus_SafeVerdictStillRendersSafe(t *testing.T) {
	var buf bytes.Buffer
	renderRecoveryStatus(&buf, "gastown", "rictus", &RecoveryStatus{
		Verdict:       polecat.WorkstateVerdictSafeToNuke,
		SafeToNuke:    true,
		CleanupStatus: polecat.CleanupClean,
		MQStatus:      "submitted",
	})
	out := buf.String()

	if !strings.Contains(out, "Safe to nuke - no work at risk.") {
		t.Errorf("a genuinely safe polecat was not reported safe:\n%s", out)
	}
	if !strings.Contains(out, "MQ Status:       submitted") {
		t.Errorf("MQ status missing from safe report:\n%s", out)
	}
}

// TestRenderRecoveryStatus_EmptyCleanupStatusIsLabeled covers the field that
// read as blank in the hq-fhb report, where "no value" looked like "nothing
// wrong".
func TestRenderRecoveryStatus_EmptyCleanupStatusIsLabeled(t *testing.T) {
	var buf bytes.Buffer
	renderRecoveryStatus(&buf, "gastown", "rictus", &RecoveryStatus{
		Verdict: polecat.WorkstateVerdictWorking,
		Branch:  "polecat/rictus/gt-85p+msucbz4m",
	})
	out := buf.String()

	if !strings.Contains(out, "Cleanup Status:  unknown (not reported)") {
		t.Errorf("empty cleanup status rendered as blank rather than unknown:\n%s", out)
	}
}

func TestRenderRecoveryStatus_ShowsBlockersAndActions(t *testing.T) {
	var buf bytes.Buffer
	renderRecoveryStatus(&buf, "gastown", "rictus", &RecoveryStatus{
		Verdict:         polecat.WorkstateVerdictNeedsRecovery,
		NeedsRecovery:   true,
		Blockers:        []string{"agent_liveness=alive session=gt-rictus"},
		RecoveryActions: recoveryActionsForBlockers([]string{"agent_liveness=alive session=gt-rictus"}),
	})
	out := buf.String()

	if !strings.Contains(out, "agent_liveness=alive session=gt-rictus") {
		t.Errorf("blocker missing from output:\n%s", out)
	}
	if !strings.Contains(out, "stop the session") {
		t.Errorf("recovery action missing from output:\n%s", out)
	}
}

type fakeLivenessProbe struct {
	available     bool
	hasSession    bool
	hasSessionErr error
	agentAlive    bool
	agentAliveErr error
}

func (f fakeLivenessProbe) IsAvailable() bool { return f.available }
func (f fakeLivenessProbe) HasSession(string) (bool, error) {
	return f.hasSession, f.hasSessionErr
}
func (f fakeLivenessProbe) IsAgentAliveChecked(string) (bool, error) {
	return f.agentAlive, f.agentAliveErr
}

// writeLock writes an identity lock with the given PID, mimicking what an agent
// entrypoint leaves in a polecat worktree.
func writeLock(t *testing.T, workerDir string, pid int) {
	t.Helper()
	runtimeDir := filepath.Join(workerDir, ".runtime")
	if err := os.MkdirAll(runtimeDir, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(lock.LockInfo{
		PID:        pid,
		AcquiredAt: time.Now(),
		SessionID:  "gt-rictus",
		PIDSource:  lock.OwnerSourceProcess,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "agent.lock"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

// TestPolecatAgentLiveness_DeadLockPIDDoesNotProveDeath is the exact real-world
// condition from gt-85p: every live polecat's lock held a dead PID (it recorded
// the short-lived `gt prime` child). Liveness must come from the session in that
// case, and the polecat must not be reported safe to nuke.
func TestPolecatAgentLiveness_DeadLockPIDDoesNotProveDeath(t *testing.T) {
	clonePath := t.TempDir()
	writeLock(t, clonePath, 999999999) // deliberately dead PID

	probe := fakeLivenessProbe{available: true, hasSession: true, agentAlive: true}
	live, diagnostic := polecatAgentLiveness(probe, "gastown", "rictus", clonePath)

	if !live.Alive {
		t.Fatalf("liveness = %+v (diagnostic %q); a running session must be observed alive despite the dead lock PID", live, diagnostic)
	}

	safe := polecat.WorkstateDisposition{Verdict: polecat.WorkstateVerdictSafeToNuke, SafeToNuke: true, Reusable: true}
	guarded := polecat.ApplyLivenessFailSafe(safe, live)
	if guarded.SafeToNuke || guarded.Verdict == polecat.WorkstateVerdictSafeToNuke {
		t.Errorf("verdict = %q safe_to_nuke = %v; a live polecat must never be safe to nuke", guarded.Verdict, guarded.SafeToNuke)
	}

	var buf bytes.Buffer
	status := &RecoveryStatus{}
	applyWorkstateDispositionToRecoveryStatus(status, guarded)
	renderRecoveryStatus(&buf, "gastown", "rictus", status)
	if claimsSafe(buf.String()) {
		t.Errorf("live polecat rendered as safe to nuke:\n%s", buf.String())
	}
}

func TestPolecatAgentLiveness_LiveLockPIDProvesLife(t *testing.T) {
	clonePath := t.TempDir()
	writeLock(t, clonePath, os.Getpid())

	// tmux says the session is gone; the live lock PID still proves life, and
	// contradictory evidence must resolve in favor of not destroying anything.
	probe := fakeLivenessProbe{available: true, hasSession: false}
	live, _ := polecatAgentLiveness(probe, "gastown", "rictus", clonePath)

	if !live.Alive {
		t.Errorf("liveness = %+v, want alive from the live lock PID", live)
	}
}

func TestPolecatAgentLiveness_Evidence(t *testing.T) {
	tests := []struct {
		name          string
		probe         agentLivenessProbe
		wantAlive     bool
		wantUnknown   bool
		wantDiagnostc string
	}{
		{
			name:      "no session is proof of death",
			probe:     fakeLivenessProbe{available: true, hasSession: false},
			wantAlive: false,
		},
		{
			name:        "session probe failure is unknown",
			probe:       fakeLivenessProbe{available: true, hasSessionErr: errors.New("tmux server unreachable")},
			wantUnknown: true,
		},
		{
			name:        "agent probe failure is unknown",
			probe:       fakeLivenessProbe{available: true, hasSession: true, agentAliveErr: errors.New("ps failed")},
			wantUnknown: true,
		},
		{
			name:      "live agent is alive",
			probe:     fakeLivenessProbe{available: true, hasSession: true, agentAlive: true},
			wantAlive: true,
		},
		{
			name:          "session without agent process is reported, not blocked",
			probe:         fakeLivenessProbe{available: true, hasSession: true, agentAlive: false},
			wantDiagnostc: "agent_liveness=dead",
		},
		{
			name:          "no tmux is unobservable, not death",
			probe:         fakeLivenessProbe{available: false},
			wantDiagnostc: "agent_liveness=unobservable",
		},
		{
			name:          "nil probe is unobservable",
			probe:         nil,
			wantDiagnostc: "agent_liveness=unobservable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Empty clone path: no lock file to consult.
			live, diagnostic := polecatAgentLiveness(tt.probe, "gastown", "rictus", "")

			if live.Alive != tt.wantAlive {
				t.Errorf("Alive = %v, want %v", live.Alive, tt.wantAlive)
			}
			if live.Unknown != tt.wantUnknown {
				t.Errorf("Unknown = %v, want %v", live.Unknown, tt.wantUnknown)
			}
			if tt.wantDiagnostc != "" && !strings.Contains(diagnostic, tt.wantDiagnostc) {
				t.Errorf("diagnostic = %q, want it to contain %q", diagnostic, tt.wantDiagnostc)
			}
			if tt.wantUnknown && live.Detail == "" {
				t.Error("unknown liveness carries no evidence detail")
			}
		})
	}
}

// TestPolecatAgentLiveness_UnobservableStaysNonBlocking documents the
// deliberate limit of the fail-safe: where liveness cannot be observed at all
// (no tmux — headless or ACP deployments), the fact is reported as a diagnostic
// and cleanup behaves exactly as it did before.
func TestPolecatAgentLiveness_UnobservableStaysNonBlocking(t *testing.T) {
	live, diagnostic := polecatAgentLiveness(fakeLivenessProbe{available: false}, "gastown", "rictus", "")
	if live.Alive || live.Unknown {
		t.Fatalf("liveness = %+v, want a non-blocking observation", live)
	}
	if diagnostic == "" {
		t.Error("unobservable liveness produced no diagnostic")
	}

	safe := polecat.WorkstateDisposition{Verdict: polecat.WorkstateVerdictSafeToNuke, SafeToNuke: true}
	if got := polecat.ApplyLivenessFailSafe(safe, live); !got.SafeToNuke {
		t.Error("unobservable liveness blocked cleanup; that is a behavior change for headless rigs")
	}
}

package polecat

import (
	"strings"
	"testing"
)

// TestApplyLivenessFailSafe_LiveAgentIsNeverSafeToNuke is the gt-85p regression
// guard: a polecat whose agent process is running must never be reported safe
// to destroy, no matter what the bead state says about it.
func TestApplyLivenessFailSafe_LiveAgentIsNeverSafeToNuke(t *testing.T) {
	safe := WorkstateDisposition{
		Verdict:     WorkstateVerdictSafeToNuke,
		Reason:      "reusable",
		Reusable:    true,
		SafeToNuke:  true,
		ReuseStatus: "idle-preserved",
	}

	got := ApplyLivenessFailSafe(safe, AgentLiveness{Alive: true, Detail: "agent_liveness=alive session=gt-rictus"})

	if got.Verdict != WorkstateVerdictNeedsRecovery {
		t.Errorf("Verdict = %q, want %q", got.Verdict, WorkstateVerdictNeedsRecovery)
	}
	if got.SafeToNuke {
		t.Error("SafeToNuke = true for a live agent")
	}
	if got.Reusable {
		t.Error("Reusable = true for a live agent")
	}
	if !got.NeedsRecovery {
		t.Error("NeedsRecovery = false for a live agent")
	}
	if !got.CountsTowardCapacity {
		t.Error("CountsTowardCapacity = false for a live agent")
	}
	if len(got.Blockers) != 1 || !strings.Contains(got.Blockers[0], "agent_liveness=alive") {
		t.Errorf("Blockers = %v, want the liveness evidence", got.Blockers)
	}
}

func TestApplyLivenessFailSafe_UnknownLivenessIsNotSafeToNuke(t *testing.T) {
	safe := WorkstateDisposition{Verdict: WorkstateVerdictSafeToNuke, SafeToNuke: true, Reusable: true}

	got := ApplyLivenessFailSafe(safe, AgentLiveness{Unknown: true, Detail: "agent_liveness=unknown session=gt-rictus error=probe failed"})

	if got.SafeToNuke || got.Verdict == WorkstateVerdictSafeToNuke {
		t.Errorf("verdict = %q safe_to_nuke = %v; unknown liveness must not authorize destruction", got.Verdict, got.SafeToNuke)
	}
	if got.Reason != "agent-liveness-unknown" {
		t.Errorf("Reason = %q, want %q", got.Reason, "agent-liveness-unknown")
	}
}

func TestApplyLivenessFailSafe_ObservedDeathKeepsVerdict(t *testing.T) {
	safe := WorkstateDisposition{
		Verdict:     WorkstateVerdictSafeToNuke,
		Reason:      "reusable",
		Reusable:    true,
		SafeToNuke:  true,
		ReuseStatus: "idle-preserved",
	}

	got := ApplyLivenessFailSafe(safe, AgentLiveness{})

	if got.Verdict != WorkstateVerdictSafeToNuke || !got.SafeToNuke || !got.Reusable {
		t.Errorf("disposition = %+v, want the original SAFE_TO_NUKE preserved when the agent is observed gone", got)
	}
}

func TestApplyLivenessFailSafe_LeavesNonSafeVerdictsAlone(t *testing.T) {
	for _, verdict := range []string{
		WorkstateVerdictWorking,
		WorkstateVerdictNeedsRecovery,
		WorkstateVerdictPendingMR,
		WorkstateVerdictNeedsMQSubmit,
	} {
		in := WorkstateDisposition{Verdict: verdict, Reason: "original", Blockers: []string{"original-blocker"}}
		got := ApplyLivenessFailSafe(in, AgentLiveness{Alive: true})
		if got.Verdict != verdict || got.Reason != "original" || len(got.Blockers) != 1 {
			t.Errorf("verdict %q was rewritten to %+v; only SAFE_TO_NUKE needs downgrading", verdict, got)
		}
	}
}

// TestDecideWorkstate_LiveWorkingPolecatWithDeadLockPID reproduces the exact
// real-world condition from gt-85p: the identity lock holds a dead PID (it
// recorded a short-lived CLI child), so nothing in the persisted state proves
// the agent is alive — but the polecat is mid-task with work on its hook.
func TestDecideWorkstate_LiveWorkingPolecatWithDeadLockPID(t *testing.T) {
	// The polecat is working: hooked bead, branch with unmerged commits.
	working := DecideWorkstate(WorkstateInput{
		State:           StateWorking,
		Branch:          "polecat/rictus/gt-85p+msucbz4m",
		HookBead:        "gt-85p",
		UnpushedCommits: 3,
	})
	if working.SafeToNuke || working.Verdict == WorkstateVerdictSafeToNuke {
		t.Fatalf("verdict = %q safe_to_nuke = %v for a working polecat", working.Verdict, working.SafeToNuke)
	}

	// Now the state that made the bug reachable: the bead says idle/clean
	// (agent_state never advanced, cleanup_status reported clean) while the
	// process is in fact alive. The lock's dead PID cannot rescue this; only a
	// positive liveness observation can.
	looksIdle := DecideWorkstate(WorkstateInput{
		State:         StateIdle,
		Branch:        "polecat/rictus/gt-85p+msucbz4m",
		CleanupStatus: CleanupClean,
	})
	if !looksIdle.SafeToNuke {
		t.Fatalf("precondition failed: expected SAFE_TO_NUKE from idle+clean state, got %+v", looksIdle)
	}

	guarded := ApplyLivenessFailSafe(looksIdle, AgentLiveness{Alive: true, Detail: "agent_liveness=alive session=gt-rictus"})
	if guarded.SafeToNuke || guarded.Verdict == WorkstateVerdictSafeToNuke {
		t.Errorf("verdict = %q safe_to_nuke = %v; a live polecat must never be reported safe to nuke", guarded.Verdict, guarded.SafeToNuke)
	}
}

// TestDecideWorkstate_UnusableCleanupStatusIsNotSafeToNuke covers the second
// required guarantee: empty, unknown, or unparseable cleanup_status is missing
// evidence, not permission to destroy.
func TestDecideWorkstate_UnusableCleanupStatusIsNotSafeToNuke(t *testing.T) {
	for _, status := range []CleanupStatus{"", CleanupUnknown, "garbage-value", "CLEAN", "clean "} {
		got := DecideWorkstate(WorkstateInput{
			State:         StateIdle,
			Branch:        "polecat/rictus/gt-85p+msucbz4m",
			CleanupStatus: status,
		})
		if got.SafeToNuke || got.Verdict == WorkstateVerdictSafeToNuke {
			t.Errorf("cleanup_status %q gave verdict %q (safe_to_nuke=%v); want a refusal", status, got.Verdict, got.SafeToNuke)
		}
		if len(got.Blockers) == 0 {
			t.Errorf("cleanup_status %q produced no blocker explaining the refusal", status)
		}
	}
}

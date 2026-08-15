package polecat

// AgentLiveness is what we could observe about a polecat's agent process.
//
// The three states are deliberately distinct. "Not alive" is only meaningful
// when it means *observed dead*; a failed probe must never be collapsed into it,
// because destructive callers read the zero value as permission to proceed.
type AgentLiveness struct {
	// Alive means the agent process was positively observed running.
	Alive bool
	// Unknown means liveness could not be determined. Not the same as dead.
	Unknown bool
	// Detail is the evidence, rendered as a blocker when it blocks.
	Detail string
}

// ApplyLivenessFailSafe downgrades a SAFE_TO_NUKE verdict when the agent is
// alive, or when liveness could not be determined.
//
// SAFE_TO_NUKE authorizes destroying a sandbox and its unmerged work, so it must
// rest on positive proof of death rather than on an absence of contrary
// evidence. gt-85p is the failure this guards: a live, mid-task polecat was
// reported "safe to nuke — no work at risk" while its branch held unmerged
// commits, because nothing on the destructive path ever asked whether the agent
// was still running.
//
// This is applied only to check-recovery's verdict, not inside DecideWorkstate:
// list, capacity, and reuse ask a different question ("is this polecat busy?")
// and must keep classifying live idle polecats as reusable.
func ApplyLivenessFailSafe(d WorkstateDisposition, live AgentLiveness) WorkstateDisposition {
	if d.Verdict != WorkstateVerdictSafeToNuke && !d.SafeToNuke {
		return d
	}
	if !live.Alive && !live.Unknown {
		return d
	}

	reason := "agent-alive"
	blocker := live.Detail
	if live.Alive {
		if blocker == "" {
			blocker = "agent_liveness=alive"
		}
	} else {
		reason = "agent-liveness-unknown"
		if blocker == "" {
			blocker = "agent_liveness=unknown"
		}
	}

	d.Verdict = WorkstateVerdictNeedsRecovery
	d.Reason = reason
	d.SafeToNuke = false
	d.Reusable = false
	d.NeedsRecovery = true
	d.CountsTowardCapacity = true
	d.ReuseStatus = "idle-recovery-needed"
	d.Blockers = append(d.Blockers, blocker)
	return d
}

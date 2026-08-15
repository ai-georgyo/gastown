package cmd

import (
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

// TestSourceIssueTerminal covers the bead-status half of the gt-87i fix: only
// a genuinely terminal bead may unlock a zero-commit completion.
func TestSourceIssueTerminal(t *testing.T) {
	tests := []struct {
		name  string
		issue *beads.Issue
		want  bool
	}{
		{"nil issue is not terminal", nil, false},
		{"closed is terminal", &beads.Issue{ID: "gt-a", Status: string(beads.StatusClosed)}, true},
		{"tombstone is terminal", &beads.Issue{ID: "gt-a", Status: string(beads.StatusTombstone)}, true},
		{"closed with surrounding space is terminal", &beads.Issue{ID: "gt-a", Status: " closed "}, true},
		{"open is not terminal", &beads.Issue{ID: "gt-a", Status: string(beads.StatusOpen)}, false},
		{"hooked is not terminal", &beads.Issue{ID: "gt-a", Status: beads.StatusHooked}, false},
		{"in_progress is not terminal", &beads.Issue{ID: "gt-a", Status: string(beads.StatusInProgress)}, false},
		{"blocked is not terminal", &beads.Issue{ID: "gt-a", Status: string(beads.StatusBlocked)}, false},
		{"deferred is not terminal", &beads.Issue{ID: "gt-a", Status: string(beads.StatusDeferred)}, false},
		{"empty status is not terminal", &beads.Issue{ID: "gt-a"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sourceIssueTerminal(tt.issue); got != tt.want {
				t.Errorf("sourceIssueTerminal(%+v) = %v, want %v", tt.issue, got, tt.want)
			}
		})
	}
}

// TestAllowZeroCommitCompletion locks in the gt-87i regression: a polecat whose
// work bead is already closed must be able to complete with zero commits ahead
// of base. Before the fix, gt done hard-failed for that polecat, so it never
// retired its session and parked in "review-needed" indefinitely (hq-w5e).
//
// The anti-sleepwalking guard (gastown#1484) must still hold for the case it
// was written for: a polecat with an OPEN bead and no commits.
func TestAllowZeroCommitCompletion(t *testing.T) {
	tests := []struct {
		name          string
		isPolecat     bool
		cleanupStatus string
		isNoMergeTask bool
		sourceClosed  bool
		want          bool
	}{
		{
			name:          "polecat with open bead and unpushed branch is blocked",
			isPolecat:     true,
			cleanupStatus: "unpushed",
			want:          false,
		},
		{
			name:          "polecat with open bead and unknown cleanup is blocked",
			isPolecat:     true,
			cleanupStatus: "unknown",
			want:          false,
		},
		{
			name:          "polecat with closed bead completes (gt-87i)",
			isPolecat:     true,
			cleanupStatus: "unpushed",
			sourceClosed:  true,
			want:          true,
		},
		{
			name:          "polecat with closed bead and unknown cleanup completes (gt-87i)",
			isPolecat:     true,
			cleanupStatus: "unknown",
			sourceClosed:  true,
			want:          true,
		},
		{
			name:          "polecat with clean cleanup status completes",
			isPolecat:     true,
			cleanupStatus: "clean",
			want:          true,
		},
		{
			name:          "polecat on no_merge/review_only bead completes",
			isPolecat:     true,
			cleanupStatus: "unpushed",
			isNoMergeTask: true,
			want:          true,
		},
		{
			name:          "non-polecat is never blocked",
			isPolecat:     false,
			cleanupStatus: "unpushed",
			want:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := allowZeroCommitCompletion(tt.isPolecat, tt.cleanupStatus, tt.isNoMergeTask, tt.sourceClosed)
			if got != tt.want {
				t.Errorf("allowZeroCommitCompletion(%v, %q, %v, %v) = %v, want %v",
					tt.isPolecat, tt.cleanupStatus, tt.isNoMergeTask, tt.sourceClosed, got, tt.want)
			}
		})
	}
}

// TestZeroCommitPolecatReachesTerminalState is the gt-87i regression test the
// bead asks for: a polecat that completes a verify-only bead with zero commits
// ahead of base must reach a terminal state without a merge ever occurring.
//
// Terminal here means both halves of the deadlock are released:
//  1. gt done is allowed to run the COMPLETED path at all, and
//  2. that path retires the polecat session (no MR is created, so mrID is
//     empty and the refinery is never woken — see TestShouldNudgeRefinery).
//
// The polecat is quartz's exact shape: branch 0 commits ahead, never pushed
// (cleanup status "unpushed"), bead closed with a no-changes reason.
func TestZeroCommitPolecatReachesTerminalState(t *testing.T) {
	closedBead := &beads.Issue{
		ID:     "AVL-gtl",
		Status: string(beads.StatusClosed),
		Notes:  "no-changes: verify-only bead",
	}

	if !sourceIssueTerminal(closedBead) {
		t.Fatalf("closed work bead should be terminal")
	}
	if !allowZeroCommitCompletion(true, "unpushed", false, sourceIssueTerminal(closedBead)) {
		t.Fatalf("polecat with a closed bead and zero commits must be allowed to complete")
	}
	if !shouldRetirePolecatSessionAfterDone(ExitCompleted, "", false, false) {
		t.Fatalf("a completed no-MR exit must retire the polecat session")
	}
	if shouldNudgeRefinery(ExitCompleted, "") {
		t.Fatalf("a no-MR completion must not emit MQ_SUBMIT")
	}

	// The open-bead case is the one the anti-sleepwalking guard exists for and
	// must stay blocked, otherwise this fix would hide lost implementation work.
	openBead := &beads.Issue{ID: "AVL-gtl", Status: string(beads.StatusOpen)}
	if allowZeroCommitCompletion(true, "unpushed", false, sourceIssueTerminal(openBead)) {
		t.Fatalf("polecat with an open bead and zero commits must still be blocked")
	}
}

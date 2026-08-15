package cmd

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/mail"
)

// storedAssignee returns the assignee value that actually lands in bd's
// assignee column when gastown writes `bd update <id> --status=hooked
// --assignee=<identity>` — i.e. after the normalization boundary has run.
func storedAssignee(t *testing.T, identity string) string {
	t.Helper()
	args := beads.NormalizeAssigneeArgs([]string{
		"update", "hq-wisp-test", "--status=hooked", "--assignee=" + identity,
	})
	for _, arg := range args {
		if value, ok := strings.CutPrefix(arg, "--assignee="); ok {
			return value
		}
	}
	t.Fatalf("no --assignee flag survived normalization of %v", args)
	return ""
}

func matchedByQuery(stored, queryIdentity string) bool {
	for _, variant := range beads.AssigneeQueryVariants(queryIdentity) {
		if variant == stored {
			return true
		}
	}
	return false
}

// TestPatrolReportAssigneeIsFindableByHookQuery is the regression test for
// hq-j5v defect 1: gt patrol report armed the next patrol wisp with assignee
// "deacon" while gt hook queried for "deacon/", so the deacon's hook looked
// empty and the patrol loop stalled silently.
//
// It asserts the round trip rather than a literal: whatever the patrol writer
// stamps on the wisp, after the write boundary, must be matched by the query
// the hook reader builds for the same role.
func TestPatrolReportAssigneeIsFindableByHookQuery(t *testing.T) {
	townRoot := t.TempDir()
	roles := []RoleInfo{
		{Role: RoleDeacon, TownRoot: townRoot},
		{Role: RoleWitness, Rig: "gastown", TownRoot: townRoot},
		{Role: RoleRefinery, Rig: "gastown", TownRoot: townRoot},
	}

	for _, roleInfo := range roles {
		t.Run(string(roleInfo.Role), func(t *testing.T) {
			cfg, err := patrolConfigForRole(roleInfo.Role, roleInfo)
			if err != nil {
				t.Fatalf("patrolConfigForRole(%s): %v", roleInfo.Role, err)
			}
			if cfg.Assignee == "" {
				t.Fatal("patrol config has no assignee")
			}

			// Reader side: gt hook resolves its own identity through
			// buildAgentIdentity (via resolveSelfTarget) and queries for it.
			hookIdentity := buildAgentIdentity(roleInfo)
			if hookIdentity == "" {
				t.Fatal("hook identity is empty")
			}

			stored := storedAssignee(t, cfg.Assignee)
			if !matchedByQuery(stored, hookIdentity) {
				t.Errorf("patrol wrote assignee %q (stored as %q); gt hook queries %v — the wisp would be invisible on the hook",
					cfg.Assignee, stored, beads.AssigneeQueryVariants(hookIdentity))
			}
		})
	}
}

// TestLegacySlashedAssigneeStaysFindable guards the migration: rows written
// before normalization still carry "deacon/", and the hook query must keep
// matching them until they age out.
func TestLegacySlashedAssigneeStaysFindable(t *testing.T) {
	for _, legacy := range []string{"deacon/", "mayor/"} {
		canonical := beads.CanonicalAssignee(legacy)
		if !matchedByQuery(legacy, canonical) {
			t.Errorf("legacy row assigned %q is not matched by a query for %q", legacy, canonical)
		}
	}
}

// TestAgentIdentityMatchesBDActor is the regression test for hq-516: bd decides
// bead ownership by comparing the stored assignee against BD_ACTOR. When the
// two disagree, an agent cannot close — or archive the mail of — its own beads.
// config.AgentEnv is the single source of truth for BD_ACTOR, so the identity
// gastown stamps on beads must agree with what it exports.
func TestAgentIdentityMatchesBDActor(t *testing.T) {
	tests := []struct {
		name     string
		envRole  string
		roleInfo RoleInfo
	}{
		{"deacon", constants.RoleDeacon, RoleInfo{Role: RoleDeacon}},
		{"mayor", constants.RoleMayor, RoleInfo{Role: RoleMayor}},
		{"witness", constants.RoleWitness, RoleInfo{Role: RoleWitness, Rig: "gastown"}},
		{"refinery", constants.RoleRefinery, RoleInfo{Role: RoleRefinery, Rig: "gastown"}},
		{"polecat", constants.RolePolecat, RoleInfo{Role: RolePolecat, Rig: "gastown", Polecat: "nux"}},
		{"crew", constants.RoleCrew, RoleInfo{Role: RoleCrew, Rig: "gastown", Polecat: "max"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := config.AgentEnv(config.AgentEnvConfig{
				Role:      tt.envRole,
				Rig:       tt.roleInfo.Rig,
				AgentName: tt.roleInfo.Polecat,
			})
			actor := env["BD_ACTOR"]
			if actor == "" {
				t.Fatalf("config.AgentEnv set no BD_ACTOR for role %q", tt.envRole)
			}

			stored := storedAssignee(t, buildAgentIdentity(tt.roleInfo))
			if !beads.SameAssignee(stored, actor) {
				t.Errorf("bead assignee %q does not match BD_ACTOR %q — bd would refuse this agent's own writes", stored, actor)
			}
		})
	}
}

// TestFormatArchiveError_NoPhantomForceFlag is the regression test for the
// second defect in hq-516: bd's ownership refusal suggests "--force", a flag
// gt mail archive does not have, so the raw text sent readers to a dead end.
func TestFormatArchiveError_NoPhantomForceFlag(t *testing.T) {
	err := &mail.NotOwnerError{ID: "hq-wisp-jfo", Assignee: "deacon/", Actor: "deacon"}
	got := formatArchiveError("hq-wisp-jfo", err)

	if strings.Contains(got, "gt mail archive --force") {
		t.Errorf("archive error suggests a gt mail archive --force flag that does not exist:\n%s", got)
	}
	for _, want := range []string{"hq-wisp-jfo", `"deacon/"`, `"deacon"`, "bd update hq-wisp-jfo --assignee deacon"} {
		if !strings.Contains(got, want) {
			t.Errorf("archive error is missing %q:\n%s", want, got)
		}
	}
}

// TestFormatArchiveError_PassesThroughOtherErrors keeps unrelated failures
// readable instead of forcing them into the ownership template.
func TestFormatArchiveError_PassesThroughOtherErrors(t *testing.T) {
	got := formatArchiveError("hq-1", mail.ErrEmptyInbox)
	if !strings.Contains(got, "hq-1") || !strings.Contains(got, mail.ErrEmptyInbox.Error()) {
		t.Errorf("unexpected passthrough rendering: %q", got)
	}
}

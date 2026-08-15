package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
)

func TestAgentBeadMatchesDescriptionAndIDFallback(t *testing.T) {
	tests := []struct {
		name  string
		issue *beads.Issue
		role  string
		rig   string
		want  bool
	}{
		{
			name: "description matches legacy random wisp ID",
			issue: &beads.Issue{
				ID:          "au-wisp-0ti",
				Description: "Agent\n\nrole_type: refinery\nrig: alleago_ui",
			},
			role: "refinery",
			rig:  "alleago_ui",
			want: true,
		},
		{
			name: "canonical ID fallback matches sparse wisp metadata",
			issue: &beads.Issue{
				ID: "gt-gastown-witness",
			},
			role: "witness",
			rig:  "gastown",
			want: true,
		},
		{
			name: "collapsed prefix-rig ID fallback matches sparse metadata",
			issue: &beads.Issue{
				ID: "cp-refinery",
			},
			role: "refinery",
			rig:  "cp",
			want: true,
		},
		{
			name: "role mismatch",
			issue: &beads.Issue{
				ID:          "gt-gastown-witness",
				Description: "Agent\n\nrole_type: witness\nrig: gastown",
			},
			role: "refinery",
			rig:  "gastown",
			want: false,
		},
		{
			name: "rig mismatch",
			issue: &beads.Issue{
				ID:          "gt-gastown-refinery",
				Description: "Agent\n\nrole_type: refinery\nrig: gastown",
			},
			role: "refinery",
			rig:  "other",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentBeadMatches(tt.issue, tt.role, tt.rig)
			if got != tt.want {
				t.Fatalf("agentBeadMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPickBestAgentBead(t *testing.T) {
	candidates := []agentBeadCandidate{
		candidate("town-issue", agentSourceTownIssues, "open"),
		candidate("rig-issue", agentSourceRigIssues, "open"),
		candidate("town-wisp", agentSourceTownWisps, "open"),
		candidate("rig-wisp", agentSourceRigWisps, "open"),
	}

	got, err := pickBestAgentBead(candidates)
	if err != nil {
		t.Fatalf("pickBestAgentBead returned error: %v", err)
	}
	if got == nil || got.ID != "rig-wisp" {
		t.Fatalf("pickBestAgentBead picked %v, want rig-wisp", got)
	}
}

func TestPickBestAgentBeadSkipsClosed(t *testing.T) {
	candidates := []agentBeadCandidate{
		candidate("closed-rig-wisp", agentSourceRigWisps, "closed"),
		candidate("open-rig-issue", agentSourceRigIssues, "open"),
	}

	got, err := pickBestAgentBead(candidates)
	if err != nil {
		t.Fatalf("pickBestAgentBead returned error: %v", err)
	}
	if got == nil || got.ID != "open-rig-issue" {
		t.Fatalf("pickBestAgentBead picked %v, want open-rig-issue", got)
	}
}

func TestPickBestAgentBeadRejectsSameRankDuplicates(t *testing.T) {
	candidates := []agentBeadCandidate{
		candidate("rig-wisp-a", agentSourceRigWisps, "open"),
		candidate("rig-wisp-b", agentSourceRigWisps, "open"),
		candidate("rig-issue", agentSourceRigIssues, "open"),
	}

	got, err := pickBestAgentBead(candidates)
	if err == nil {
		t.Fatalf("pickBestAgentBead picked %v, want duplicate error", got)
	}
	if !strings.Contains(err.Error(), "multiple matching agent beads") {
		t.Fatalf("error = %q, want duplicate diagnostic", err)
	}
}

// TestRunAgentsResolveAcceptsTownBeadFromRigCwd reproduces hq-ej2: witness and
// refinery agent beads live only in the town database, and the patrol formulas
// resolve them from the agent's rig worktree. The rig cwd is the condition that
// hid this bug — resolution from the town root always worked — so the test runs
// the resolver from a rig work dir whose bead database has no agent beads.
func TestRunAgentsResolveAcceptsTownBeadFromRigCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell fake bd")
	}

	rigWorkDir := setupTownOnlyAgentBeadWorkspace(t, "gt-gastown-witness")

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(rigWorkDir); err != nil {
		t.Fatalf("chdir rig work dir: %v", err)
	}

	oldRole, oldRig, oldJSON, oldQuiet := agentsResolveRole, agentsResolveRig, agentsResolveJSON, agentsResolveQuiet
	t.Cleanup(func() {
		agentsResolveRole, agentsResolveRig = oldRole, oldRig
		agentsResolveJSON, agentsResolveQuiet = oldJSON, oldQuiet
	})
	agentsResolveRole = "witness"
	agentsResolveRig = "gastown"
	agentsResolveQuiet = false

	t.Run("plain output", func(t *testing.T) {
		agentsResolveJSON = false
		var out bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&out)

		if err := runAgentsResolve(cmd, nil); err != nil {
			t.Fatalf("runAgentsResolve() error = %v", err)
		}
		if got := strings.TrimSpace(out.String()); got != "gt-gastown-witness" {
			t.Fatalf("runAgentsResolve() printed %q, want gt-gastown-witness", got)
		}
	})

	t.Run("json reports town provenance", func(t *testing.T) {
		agentsResolveJSON = true
		defer func() { agentsResolveJSON = false }()
		var out bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&out)

		if err := runAgentsResolve(cmd, nil); err != nil {
			t.Fatalf("runAgentsResolve() error = %v", err)
		}
		var result agentsResolveResult
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("parsing resolve JSON %q: %v", out.String(), err)
		}
		if result.ID != "gt-gastown-witness" {
			t.Fatalf("resolve JSON id = %q, want gt-gastown-witness", result.ID)
		}
		if result.Source != string(agentSourceTownIssues) {
			t.Fatalf("resolve JSON source = %q, want %q", result.Source, agentSourceTownIssues)
		}
	})
}

// setupTownOnlyAgentBeadWorkspace builds a town whose only agent bead lives in
// the town database, installs a fake bd that serves it, and returns the rig
// work dir to run from.
func setupTownOnlyAgentBeadWorkspace(t *testing.T, agentBeadID string) string {
	t.Helper()

	tmp := t.TempDir()
	townRoot := filepath.Join(tmp, "gt")
	townBeads := filepath.Join(townRoot, ".beads")
	rigWorkDir := filepath.Join(townRoot, "gastown", "mayor", "rig")
	rigBeads := filepath.Join(rigWorkDir, ".beads")

	for _, dir := range []string{filepath.Join(townRoot, "mayor"), townBeads, rigBeads} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(townRoot, "mayor", "town.json"), []byte(`{"name":"gt"}`), 0o644); err != nil {
		t.Fatalf("write town.json: %v", err)
	}

	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	// The fake bd answers agent-bead lookups only for the town database, so a
	// rig-local hit cannot mask the fallback under test. bd may be invoked with
	// a leading --allow-stale (probed at runtime), so strip it first.
	bdScript := `#!/bin/sh
if [ "$1" = "--allow-stale" ]; then shift; fi
case "$1" in
  version)
    printf 'bd 1.0.0-fake\n'
    ;;
  list)
    if [ "${BEADS_DIR-}" = "` + townBeads + `" ]; then
      cat <<'AGENT_JSON'
[{"id":"` + agentBeadID + `","status":"open","description":"Agent\n\nrole_type: witness\nrig: gastown"}]
AGENT_JSON
    else
      printf '[]\n'
    fi
    ;;
  query)
    printf '[]\n'
    ;;
  *)
    printf 'unexpected bd command: %s\n' "$1" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(bdScript), 0o755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The inherited town BEADS_DIR must not be what makes this work: the rig
	// cwd is what the agents actually run from.
	t.Setenv("BEADS_DIR", townBeads)

	return rigWorkDir
}

func candidate(id string, source agentBeadSource, status string) agentBeadCandidate {
	return agentBeadCandidate{
		ID:     id,
		Source: source,
		Status: status,
		Issue:  &beads.Issue{ID: id, Status: status},
	}
}

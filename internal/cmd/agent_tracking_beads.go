package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/steveyegge/gastown/internal/beads"
)

// findCwdBeadsWorkDir finds the nearest .beads directory by walking up from CWD.
// It intentionally ignores BEADS_DIR for callers whose target is implied by
// the current rig worktree rather than inherited session environment.
func findCwdBeadsWorkDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	path := cwd
	for {
		if _, err := os.Stat(filepath.Join(path, ".beads")); err == nil {
			return path, nil
		}

		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}

	return "", fmt.Errorf("no .beads directory found")
}

// resolveAgentTrackingBeadsDir resolves the bead database used for agent state.
// Agent tracking follows the agent's current rig, so cwd-local redirects must
// win over an inherited town-level BEADS_DIR. The env-first resolver remains a
// fallback for contexts that do not have a cwd-local .beads directory.
func resolveAgentTrackingBeadsDir() (string, error) {
	workDir, err := findCwdBeadsWorkDir()
	if err != nil {
		workDir, err = findLocalBeadsDir()
	}
	if err != nil {
		return "", err
	}

	beadsDir := beads.ResolveBeadsDir(workDir)
	if beadsDir == "" {
		return "", fmt.Errorf("not in a beads workspace")
	}
	return beadsDir, nil
}

// townAgentBeadsDir returns the town bead database for the current working
// directory, or "" when the cwd is not inside a Gas Town workspace.
func townAgentBeadsDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	townRoot := beads.FindTownRoot(cwd)
	if townRoot == "" {
		return ""
	}
	return beads.ResolveBeadsDir(beads.GetTownBeadsPath(townRoot))
}

// resolveAgentBeadLabels reads all labels for an agent bead, preferring the
// rig-local tracking database and falling back to the town database.
//
// Witness and refinery agent beads are provisioned only in the town database,
// but those agents run from a rig worktree, so the cwd-local lookup misses and
// backoff state becomes unreadable and unwritable from the only cwd that uses
// it (hq-ej2). The returned beads dir is the database that answered, so
// callers pin follow-up writes to the same place the state was read from.
//
// On failure the rig-local error is returned, since that lookup is the primary
// one and its diagnostic names the database the caller expected.
func resolveAgentBeadLabels(agentBead, preferredDir string) ([]string, string, error) {
	labels, err := getAllAgentLabels(agentBead, preferredDir)
	if err == nil {
		return labels, preferredDir, nil
	}

	townDir := townAgentBeadsDir()
	if townDir == "" || filepath.Clean(townDir) == filepath.Clean(preferredDir) {
		return nil, preferredDir, err
	}

	townLabels, townErr := getAllAgentLabels(agentBead, townDir)
	if townErr != nil {
		return nil, preferredDir, err
	}
	return townLabels, townDir, nil
}

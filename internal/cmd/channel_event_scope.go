package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/channelevents"
	"github.com/steveyegge/gastown/internal/workspace"
)

// resolveEventTownRoot finds the town root for channel event I/O, falling back
// to ~/gt when the command is run outside a workspace.
func resolveEventTownRoot() string {
	townRoot, err := workspace.FindFromCwd()
	if err != nil || townRoot == "" {
		home, _ := os.UserHomeDir()
		townRoot = filepath.Join(home, "gt")
	}
	return townRoot
}

// resolveEventRig determines which rig's copy of a channel a command should
// use. Rig-scoped channels (refinery, witness) have one directory per rig so
// that one rig's events never wake — or get consumed by — another rig's agent
// (gt-em1). Town-level channels always resolve to the empty rig.
//
// Resolution order for a rig-scoped channel: the explicit --rig flag, the rig
// containing the working directory, then $GT_RIG. Failing to resolve is an
// error rather than a silent fall back to the town-level directory: falling
// back would re-create the cross-rig crosstalk this scoping exists to prevent.
func resolveEventRig(townRoot, explicitRig, channel string) (string, error) {
	explicitRig = strings.TrimSpace(explicitRig)
	if explicitRig != "" {
		if !channelevents.ValidRigName.MatchString(explicitRig) {
			return "", fmt.Errorf("invalid rig name %q: must match [a-zA-Z0-9_-]", explicitRig)
		}
		return explicitRig, nil
	}
	if !channelevents.IsRigScoped(channel) {
		return "", nil
	}
	if cwd, err := os.Getwd(); err == nil {
		if rigName := detectRigFromPath(townRoot, cwd); rigName != "" {
			return rigName, nil
		}
	}
	if rigName := strings.TrimSpace(os.Getenv("GT_RIG")); rigName != "" {
		return rigName, nil
	}
	return "", fmt.Errorf("channel %q is rig-scoped but the rig could not be determined "+
		"(not inside a rig directory and GT_RIG is unset): pass --rig <name>", channel)
}

// resolveEventDir resolves the on-disk directory for a channel, applying rig
// scoping. Returns the directory and the rig it was scoped to ("" for
// town-level channels).
func resolveEventDir(townRoot, explicitRig, channel string) (dir string, rigName string, err error) {
	rigName, err = resolveEventRig(townRoot, explicitRig, channel)
	if err != nil {
		return "", "", err
	}
	dir, err = channelevents.Dir(townRoot, rigName, channel)
	if err != nil {
		return "", "", err
	}
	return dir, rigName, nil
}

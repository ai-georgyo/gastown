package lock

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/steveyegge/gastown/internal/tmux"
)

// Owner sources, recorded in the lock file so readers can tell whether the PID
// identifies the long-lived agent or only the process that wrote the lock.
const (
	// OwnerSourceProcess means the PID is the writing process itself. The
	// writer may be a short-lived CLI invocation, so a dead PID from this
	// source is NOT proof that the agent died.
	OwnerSourceProcess = "process"

	// OwnerSourceTmuxPane means the PID is the tmux pane process that hosts
	// the agent. It lives as long as the agent session does, so a dead PID
	// from this source is meaningful evidence the agent is gone.
	OwnerSourceTmuxPane = "tmux-pane"
)

// Owner identifies the process that owns a worker identity.
type Owner struct {
	// PID is the process to check for liveness.
	PID int
	// SessionID is the agent's session identifier (tmux session name).
	SessionID string
	// PaneID is the tmux pane the agent runs in, when known.
	PaneID string
	// Source records how PID was determined (see OwnerSource* constants).
	Source string
}

// ResolveAgentOwner determines the long-lived agent process behind the current
// invocation.
//
// Agent entrypoints run as short-lived children of the agent (e.g. `gt prime`
// invoked from Claude's SessionStart hook). Recording os.Getpid() there writes a
// PID that is dead seconds later, which makes every live agent's lock look stale
// and lets destructive consumers conclude the agent is gone (gt-85p / hq-fhb).
//
// When running inside tmux we resolve the pane's own process instead: it hosts
// the agent and outlives every CLI child. The tmux session NAME is recorded as
// the session id — TMUX_PANE is a pane id ("%20"), not a session id, and storing
// it in SessionID is what made lock/session cross-checks unreliable.
//
// Falls back to the calling process when tmux cannot be consulted; Source then
// says "process" so readers know the PID is not agent-anchored.
func ResolveAgentOwner(fallbackSessionID string) Owner {
	paneID := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	fallback := Owner{
		PID:       os.Getpid(),
		SessionID: fallbackSessionID,
		PaneID:    paneID,
		Source:    OwnerSourceProcess,
	}
	if paneID == "" {
		return fallback
	}

	sessionName, panePID, err := tmuxPaneOwner(paneID)
	if err != nil || panePID <= 0 {
		return fallback
	}
	if sessionName == "" {
		sessionName = fallbackSessionID
	}
	return Owner{
		PID:       panePID,
		SessionID: sessionName,
		PaneID:    paneID,
		Source:    OwnerSourceTmuxPane,
	}
}

// tmuxPaneOwner returns the session name and pane process PID for a pane id.
// Overridable for tests.
var tmuxPaneOwner = func(paneID string) (string, int, error) {
	args := []string{}
	if sock := tmux.GetDefaultSocket(); sock != "" {
		args = append(args, "-L", sock)
	}
	args = append(args, "display-message", "-p", "-t", paneID, "#{session_name}\t#{pane_pid}")

	out, err := execCommand("tmux", args...).Output()
	if err != nil {
		return "", 0, err
	}
	return parsePaneOwner(string(out))
}

// parsePaneOwner parses a "session_name\tpane_pid" line from tmux.
// Rejects output that still contains a format string: tmux echoes the format
// verbatim when it cannot expand it, and storing an unexpanded "#{session_name}"
// (or a literal "%N" in the session field) is the class of bug this replaces.
func parsePaneOwner(out string) (string, int, error) {
	line := strings.TrimSpace(out)
	if line == "" {
		return "", 0, fmt.Errorf("empty tmux response")
	}
	if strings.Contains(line, "#{") {
		return "", 0, fmt.Errorf("unexpanded tmux format in response: %q", line)
	}

	parts := strings.Split(line, "\t")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("unexpected tmux response: %q", line)
	}

	sessionName := strings.TrimSpace(parts[0])
	pid, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return "", 0, fmt.Errorf("parsing pane pid from %q: %w", line, err)
	}
	if pid <= 0 {
		return "", 0, fmt.Errorf("invalid pane pid in %q", line)
	}
	return sessionName, pid, nil
}

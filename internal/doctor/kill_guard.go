package doctor

import (
	"strings"

	"github.com/steveyegge/gastown/internal/events"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/tmux"
)

// sessionTerminator is the destructive half of the cleanup checks: the liveness
// probe they consult and the kill they perform. It exists as an interface so
// tests can assert on the kill NOT happening, which is the only assertion that
// proves a kill-path guard works.
type sessionTerminator interface {
	// IsAgentAliveChecked reports whether an agent is running in the session.
	// The error is preserved so callers can distinguish "not alive" from
	// "could not tell".
	IsAgentAliveChecked(sess string) (bool, error)
	// Kill logs a pre-death event and then kills the session and all of its
	// descendant processes.
	Kill(sess, reason string) error
}

// tmuxTerminator is the production sessionTerminator.
type tmuxTerminator struct {
	t *tmux.Tmux
}

func newTmuxTerminator() *tmuxTerminator {
	return &tmuxTerminator{t: tmux.NewTmux()}
}

func (x *tmuxTerminator) IsAgentAliveChecked(sess string) (bool, error) {
	return x.t.IsAgentAliveChecked(sess)
}

func (x *tmuxTerminator) Kill(sess, reason string) error {
	// Log pre-death event for crash investigation (before killing).
	_ = events.LogFeed(events.TypeSessionDeath, sess,
		events.SessionDeathPayload(sess, "unknown", reason, "gt doctor"))
	// Use KillSessionWithProcesses to ensure all descendant processes are killed.
	return x.t.KillSessionWithProcesses(sess)
}

// killProtection is the verdict of the kill-path guard for one session.
type killProtection struct {
	// Protected is true when the session must NOT be auto-killed.
	Protected bool
	// Reason names the guard that protected it (empty when not protected).
	Reason string
}

// classifySessionForKill decides whether a session may be destroyed by an
// automatic cleanup fix. It FAILS CLOSED: a session it cannot positively
// classify as disposable is protected.
//
// This is the guard that gt-550, gt-fga and gt-tdk were all about: uncertainty
// on a destructive path must not be resolved toward destruction. Refusing to
// kill a session we do not understand costs a stale tmux session that a human
// can remove with one command; killing one we did not understand costs
// whatever uncommitted work was inside it, because KillSessionWithProcesses
// takes every descendant process with it.
//
// See crewProtection for the checks it layers on top of.
func classifySessionForKill(sess string) killProtection {
	if p := crewProtection(sess); p.Protected {
		return p
	}

	// Whatever this is, we could not read it. That is precisely the input the
	// checks above cannot vouch for, so it is protected.
	if _, err := isCrewSession(sess); err != nil {
		return killProtection{Protected: true, Reason: "unparseable session name: " + err.Error()}
	}

	return killProtection{}
}

// crewProtection is the crew half of the guard on its own, for the cleanup
// fixes whose targets are actively harmful (linked panes cause crosstalk, a
// session on the wrong socket breaks nudge and discovery). Those cannot adopt
// the "unparseable means protected" rule without leaving the damage in place —
// the session they most need to kill is often exactly the one that will not
// resolve. They can still refuse to trade away a human's uncommitted work.
//
// It runs two INDEPENDENT checks, in the sense that they fail differently: the
// first reads the session's identity through the prefix registry, the second
// reads the raw name and needs no registry at all. Calling one predicate twice
// (what the old "double-check" comment in zombie_check.go described) is not
// redundancy — the second call shares the first's failure mode exactly.
func crewProtection(sess string) killProtection {
	// Check 1 (structural): parse the name into an identity.
	if crew, err := isCrewSession(sess); err == nil && crew {
		return killProtection{Protected: true, Reason: "crew session (human-managed)"}
	}

	// Check 2 (lexical): read the raw name, independent of the registry.
	// A name can parse cleanly and still be crew: with prefix "gt" registered,
	// the long-form "gt-gastown-crew-joe" parses as a POLECAT named
	// "gastown-crew-joe", so check 1 clears it for killing. The town feed
	// records 36 such kills. Anything carrying a "crew" segment is treated as
	// crew regardless of what the parser made of it.
	if hasCrewSegment(sess) {
		return killProtection{Protected: true, Reason: "session name contains a crew segment"}
	}

	return killProtection{}
}

// isCrewSession reports whether the session parses as a crew session. The
// parse error is returned rather than collapsed into the bool, so callers on a
// destructive path have to decide what "I could not tell" means for them
// instead of inheriting "not crew" by default.
func isCrewSession(sess string) (bool, error) {
	identity, err := session.ParseSessionName(sess)
	if err != nil {
		return false, err
	}
	return identity.Role == session.RoleCrew, nil
}

// hasCrewSegment reports whether any dash-separated segment of the session name
// is "crew". Deliberately registry-free and deliberately over-inclusive: a
// polecat that happens to be named "crew" surviving a cleanup pass is a cheaper
// mistake than a crew session being killed.
func hasCrewSegment(sess string) bool {
	for _, seg := range strings.Split(sess, "-") {
		if seg == "crew" {
			return true
		}
	}
	return false
}

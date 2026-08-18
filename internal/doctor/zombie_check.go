package doctor

import (
	"fmt"

	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/tmux"
)

// ZombieSessionCheck detects tmux sessions that are valid Gas Town sessions
// but have no Claude/node process running inside (zombies).
// These occur when Claude exits or crashes but the tmux session remains.
type ZombieSessionCheck struct {
	FixableCheck
	sessionLister  SessionLister     // nil means "use tmux" (injected in tests)
	terminator     sessionTerminator // nil means "use tmux" (injected in tests)
	zombieSessions []string          // Cached during Run for use in Fix
}

// NewZombieSessionCheck creates a new zombie session check.
func NewZombieSessionCheck() *ZombieSessionCheck {
	return &ZombieSessionCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "zombie-sessions",
				CheckDescription: "Detect tmux sessions with dead Claude processes",
				CheckCategory:    CategoryCleanup,
			},
		},
	}
}

// NewZombieSessionCheckWithDeps creates a check with injected dependencies (for testing).
func NewZombieSessionCheckWithDeps(lister SessionLister, term sessionTerminator) *ZombieSessionCheck {
	check := NewZombieSessionCheck()
	check.sessionLister = lister
	check.terminator = term
	return check
}

func (c *ZombieSessionCheck) lister() SessionLister {
	if c.sessionLister != nil {
		return c.sessionLister
	}
	return &realSessionLister{t: tmux.NewTmux()}
}

func (c *ZombieSessionCheck) term() sessionTerminator {
	if c.terminator != nil {
		return c.terminator
	}
	return newTmuxTerminator()
}

// Run checks for zombie Gas Town sessions (tmux alive but Claude dead).
func (c *ZombieSessionCheck) Run(ctx *CheckContext) *CheckResult {
	term := c.term()

	sessions, err := c.lister().ListSessions()
	if err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: "Could not list tmux sessions",
			Details: []string{err.Error()},
		}
	}

	if len(sessions) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No tmux sessions found",
		}
	}

	// Check each Gas Town session for zombie status
	var zombies []string
	var healthyCount int

	for _, sess := range sessions {
		if sess == "" {
			continue
		}

		// Only check Gas Town sessions
		if !session.IsKnownSession(sess) {
			continue
		}

		// Skip protected sessions - crew workers are human-managed and may
		// intentionally have no Claude running (e.g., between work
		// assignments), and a session we cannot classify is never a
		// cleanup candidate.
		if p := classifySessionForKill(sess); p.Protected {
			continue
		}

		// Check if Claude is running in this session. A probe that ERRORS has
		// not established death — count the session healthy rather than
		// queueing it for the kill path.
		if alive, err := term.IsAgentAliveChecked(sess); err != nil || alive {
			healthyCount++
		} else {
			zombies = append(zombies, sess)
		}
	}

	// Cache zombies for Fix
	c.zombieSessions = zombies

	if len(zombies) == 0 {
		msg := "No zombie sessions found"
		if healthyCount > 0 {
			msg = fmt.Sprintf("All %d Gas Town sessions have running Claude processes", healthyCount)
		}
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: msg,
		}
	}

	details := make([]string, len(zombies))
	for i, session := range zombies {
		details[i] = fmt.Sprintf("Zombie: %s (tmux alive, Claude dead)", session)
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("Found %d zombie session(s)", len(zombies)),
		Details: details,
		FixHint: "Run 'gt doctor --fix' to kill zombie sessions",
	}
}

// Fix kills all zombie sessions (tmux sessions with no Claude running).
// Every guard on this path fails CLOSED: crew sessions, sessions whose name
// cannot be classified, and sessions whose liveness cannot be established are
// all left alone. See classifySessionForKill.
func (c *ZombieSessionCheck) Fix(ctx *CheckContext) error {
	if len(c.zombieSessions) == 0 {
		return nil
	}

	term := c.term()
	var lastErr error

	for _, sess := range c.zombieSessions {
		// SAFEGUARD: never auto-kill a session the guard protects.
		if p := classifySessionForKill(sess); p.Protected {
			fmt.Printf("  Not killing %s: %s\n", sess, p.Reason)
			continue
		}

		// TOCTOU guard: re-verify Claude is still dead in this session.
		// Between Run() identifying zombies and Fix() killing them,
		// a Claude process may have started (e.g., session was restarted).
		//
		// This is a liveness probe, not a crew check, and it fails in the same
		// direction as the guard above did: IsAgentAlive() reports false both
		// for "no agent running" and for "could not ask" (tmux gone, server
		// down). Use the Checked variant so an unanswered probe blocks the
		// kill instead of authorising it.
		if alive, err := term.IsAgentAliveChecked(sess); err != nil || alive {
			continue
		}

		if err := term.Kill(sess, "zombie cleanup"); err != nil {
			lastErr = err
		}
	}

	return lastErr
}

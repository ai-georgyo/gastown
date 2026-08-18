package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/channelevents"
	"github.com/steveyegge/gastown/internal/style"
)

var (
	awaitEventChannel              string
	awaitEventTimeout              string
	awaitEventBackoffBase          string
	awaitEventBackoffMult          int
	awaitEventBackoffMax           string
	awaitEventQuiet                bool
	awaitEventAgentBead            string
	awaitEventCleanup              bool
	awaitEventContextCheckInterval string
	awaitEventRig                  string
)

// validChannelName is a convenience alias for the canonical regex in channelevents.
var validChannelName = channelevents.ValidChannelName

var moleculeAwaitEventCmd = &cobra.Command{
	Use:   "await-event",
	Short: "Wait for a file-based event on a named channel",
	Long: `Wait for event files to appear in a channel directory, with optional backoff.

Unlike await-signal (which subscribes to the generic beads activity feed),
await-event watches a dedicated event channel directory for .event files.
Events are emitted via "gt mol step emit-event" or programmatically.

Channels are single-consumer: only one process should watch a given channel
at a time. If multiple consumers watch the same channel with --cleanup,
events may be deleted before all consumers read them.

CHANNEL SCOPE:
Rig-scoped channels (refinery, witness) have one directory per rig, so each
rig's agent is the sole consumer of its own events:
  ~/gt/events/rigs/<rig>/<channel>/
The rig comes from --rig, else the working directory, else $GT_RIG.
Town-level channels (mayor, deacon, custom names) stay town-global:
  ~/gt/events/<channel>/

MIGRATION DRAIN:
A rig-scoped consumer also drains the pre-scoping flat directory
~/gt/events/<channel>/, so events from a producer still running an older
binary are not stranded. An event there addressed to another rig is left
alone; one addressed to nobody (written before producers stamped the rig) is
shown to each rig's consumer once and never deleted on delivery, since no
consumer can tell whom it was meant for. --cleanup sweeps those on age
instead, once they are a day old.

EVENT FORMAT:
Events are JSON files named *.event in the channel directory:
  {"type": "...", "channel": "...", "rig": "...", "timestamp": "...", "payload": {...}}

BEHAVIOR:
1. Check for already-pending events (return immediately if found)
2. If none, poll the directory until a new .event file appears or timeout
3. On wake, return all pending event file paths and contents
4. With --cleanup, delete processed event files automatically

BACKOFF MODE:
Same as await-signal: base * multiplier^idle_cycles, capped at max.
Idle cycles and backoff-until timestamp tracked on agent bead labels.
If killed and restarted, backoff resumes from the stored backoff-until.

CONTEXT-YIELD:
When --context-check-interval is set, await-event returns early with reason
"context-yield" after the specified wall-clock interval, even if no event
arrived and the backoff timeout has not expired. This allows patrol agents
to assess context usage between waits, preventing unbounded accumulation
during long idle periods.

Output when yielding:
  CONTEXT: check
  EFFORT: full

After context-check, call await-event again with the same parameters if
context is acceptable, or hand off the session if context is high.

EXIT CODES:
  0 - Event(s) found, timeout, or context-yield
  1 - Error

EXAMPLES:
  # Wait for refinery events with 10min timeout
  gt mol step await-event --channel refinery --timeout 10m

  # Backoff mode with agent bead tracking
  gt mol step await-event --channel refinery --agent-bead VAS-refinery \
    --backoff-base 60s --backoff-mult 2 --backoff-max 10m

  # Auto-cleanup processed events
  gt mol step await-event --channel refinery --cleanup

  # Yield every 5m for context check during long idle waits
  gt mol step await-event --channel refinery --agent-bead VAS-refinery \
    --backoff-base 60s --backoff-mult 2 --backoff-max 15m --cleanup \
    --context-check-interval 5m`,
	RunE: runMoleculeAwaitEvent,
}

// AwaitEventResult is the result of an await-event operation.
type AwaitEventResult struct {
	Reason      string        `json:"reason"`                // "event" or "timeout"
	Elapsed     time.Duration `json:"elapsed"`               // how long we waited
	Events      []EventFile   `json:"events,omitempty"`      // event files found
	IdleCycles  int           `json:"idle_cycles,omitempty"` // current idle cycle count
	EffortLevel string        `json:"effort_level"`          // "full" or "abbreviated"
}

// EventFile represents a single event file.
type EventFile struct {
	Path    string          `json:"path"`
	Content json.RawMessage `json:"content"`
	// Legacy marks an event read from the pre-rig-scoping flat directory
	// rather than from this consumer's own one (gt-1qe).
	Legacy bool `json:"legacy,omitempty"`

	// retain suppresses --cleanup for this file: an unaddressed legacy event
	// is broadcast to every rig's consumer, so deleting it on delivery would
	// starve the rig it was actually meant for. Swept on age instead.
	retain bool
	// modTime orders legacy events against the delivery cursor.
	modTime time.Time
}

func init() {
	moleculeAwaitEventCmd.Flags().StringVar(&awaitEventChannel, "channel", "",
		"Event channel name (required, e.g., 'refinery')")
	moleculeAwaitEventCmd.Flags().StringVar(&awaitEventTimeout, "timeout", "60s",
		"Maximum time to wait for event (e.g., 30s, 5m, 10m)")
	moleculeAwaitEventCmd.Flags().StringVar(&awaitEventBackoffBase, "backoff-base", "",
		"Base interval for exponential backoff (e.g., 60s)")
	moleculeAwaitEventCmd.Flags().IntVar(&awaitEventBackoffMult, "backoff-mult", 2,
		"Multiplier for exponential backoff (default: 2)")
	moleculeAwaitEventCmd.Flags().StringVar(&awaitEventBackoffMax, "backoff-max", "",
		"Maximum interval cap for backoff (e.g., 10m)")
	moleculeAwaitEventCmd.Flags().StringVar(&awaitEventAgentBead, "agent-bead", "",
		"Agent bead ID for tracking idle cycles")
	moleculeAwaitEventCmd.Flags().BoolVar(&awaitEventQuiet, "quiet", false,
		"Suppress output (for scripting)")
	moleculeAwaitEventCmd.Flags().BoolVar(&awaitEventCleanup, "cleanup", false,
		"Delete event files after reading them")
	moleculeAwaitEventCmd.Flags().StringVar(&awaitEventContextCheckInterval, "context-check-interval", "",
		"Yield after this wall-clock interval so the caller can assess context (e.g., 5m). Returns reason 'context-yield'.")
	moleculeAwaitEventCmd.Flags().StringVar(&awaitEventRig, "rig", "",
		"Rig owning the channel (rig-scoped channels only; defaults to the rig of the working directory)")
	moleculeAwaitEventCmd.Flags().BoolVar(&moleculeJSON, "json", false,
		"Output as JSON")
	_ = moleculeAwaitEventCmd.MarkFlagRequired("channel")

	moleculeStepCmd.AddCommand(moleculeAwaitEventCmd)
}

func runMoleculeAwaitEvent(cmd *cobra.Command, args []string) error {
	// Validate channel name (prevent path traversal)
	if !validChannelName.MatchString(awaitEventChannel) {
		return fmt.Errorf("invalid channel name %q: must match [a-zA-Z0-9_-]", awaitEventChannel)
	}

	// Resolve event directory. Rig-scoped channels resolve to the rig's own
	// directory so this agent only sees events addressed to its rig (gt-em1).
	townRoot := resolveEventTownRoot()
	eventDir, rigName, err := resolveEventDir(townRoot, awaitEventRig, awaitEventChannel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(eventDir, 0755); err != nil {
		return fmt.Errorf("creating event directory: %w", err)
	}
	// Also drain the pre-rig-scoping directory for the migration window, so
	// events from a producer that has not been upgraded still arrive (gt-1qe).
	reader := newChannelReader(townRoot, eventDir, rigName, awaitEventChannel)

	// Read current idle cycles and backoff window from agent bead
	var idleCycles int
	var backoffUntil time.Time
	var beadsDir string
	if awaitEventAgentBead != "" {
		var wdErr error
		beadsDir, wdErr = resolveAgentTrackingBeadsDir()
		if wdErr == nil {
			allLabels, resolvedDir, labErr := resolveAgentBeadLabels(awaitEventAgentBead, beadsDir)
			if labErr != nil {
				if !awaitEventQuiet {
					fmt.Printf("%s Could not read agent bead (starting at idle=0): %v\n",
						style.Dim.Render("⚠"), labErr)
				}
			} else {
				// Pin the state writes below to the database the state came from.
				beadsDir = resolvedDir
				labels := agentStateLabels(allLabels)
				if idleStr, ok := labels["idle"]; ok {
					if n, parseErr := parseIntSimple(idleStr); parseErr == nil {
						idleCycles = n
					}
				}
				if untilStr, ok := labels["backoff-until"]; ok {
					if ts, parseErr := parseIntSimple(untilStr); parseErr == nil && ts > 0 {
						backoffUntil = time.Unix(int64(ts), 0)
					}
				}
			}
		}
	}

	// Calculate timeout (with backoff if configured)
	fullTimeout, err := calculateEventTimeout(idleCycles)
	if err != nil {
		return fmt.Errorf("invalid timeout configuration: %w", err)
	}

	// Parse context-check interval (optional)
	var contextCheckInterval time.Duration
	if awaitEventContextCheckInterval != "" {
		contextCheckInterval, err = time.ParseDuration(awaitEventContextCheckInterval)
		if err != nil {
			return fmt.Errorf("invalid context-check-interval: %w", err)
		}
	}

	// Resume from backoff-until if interrupted (same pattern as await-signal)
	timeout := fullTimeout
	resumed := false
	now := time.Now()
	if awaitEventAgentBead != "" && !backoffUntil.IsZero() && backoffUntil.After(now) {
		remaining := backoffUntil.Sub(now)
		if remaining <= fullTimeout {
			timeout = remaining
			resumed = true
			if !awaitEventQuiet && !moleculeJSON {
				fmt.Printf("%s Resuming backoff window (%v remaining)\n",
					style.Dim.Render("↻"), remaining.Round(time.Second))
			}
		}
	}

	// Persist backoff-until for crash recovery.
	// When resuming an existing window, keep the original deadline stable across
	// context-yield re-entry instead of rewriting it on every invocation.
	if awaitEventAgentBead != "" && beadsDir != "" && !resumed {
		_ = setAgentBackoffUntil(awaitEventAgentBead, beadsDir, now.Add(timeout))
	}

	if !awaitEventQuiet && !moleculeJSON {
		fmt.Printf("%s Awaiting event on channel %q (timeout: %v, idle: %d)...\n",
			style.Dim.Render("⏳"), awaitEventChannel, timeout, idleCycles)
	}

	startTime := time.Now()

	// Wait for events
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result, err := waitForChannelEvents(ctx, reader, contextCheckInterval)
	if err != nil {
		return fmt.Errorf("event watch failed: %w", err)
	}
	result.Elapsed = time.Since(startTime)

	// Update agent bead idle cycles and heartbeat
	if awaitEventAgentBead != "" && beadsDir != "" {
		// Always update heartbeat (both event and timeout) so witness doesn't
		// think we're dead during long idle periods.
		_ = updateAgentHeartbeat(awaitEventAgentBead, beadsDir)

		if result.Reason == "timeout" {
			newIdle := idleCycles + 1
			if setErr := setAgentIdleCycles(awaitEventAgentBead, beadsDir, newIdle); setErr != nil {
				if !awaitEventQuiet {
					fmt.Printf("%s Failed to update idle count: %v\n",
						style.Dim.Render("⚠"), setErr)
				}
			} else {
				result.IdleCycles = newIdle
			}
		} else if result.Reason == "event" {
			// Reset idle on event received
			if idleCycles > 0 {
				_ = setAgentIdleCycles(awaitEventAgentBead, beadsDir, 0)
			}
			result.IdleCycles = 0
		}
		// For "context-yield": idle cycles unchanged — we yielded early for context
		// assessment, not because the full backoff window elapsed.

		// Keep the backoff window across context-yield so the next invocation
		// resumes the remaining wait instead of restarting the same idle tier.
		if result.Reason == "event" || result.Reason == "timeout" {
			_ = clearAgentBackoffUntil(awaitEventAgentBead, beadsDir)
		}
	}

	// Cleanup event files if requested
	if awaitEventCleanup && result.Reason == "event" {
		for _, ef := range result.Events {
			if ef.retain {
				// Unaddressed legacy event: other rigs' consumers have not
				// seen it yet and cannot be told apart from its addressee.
				continue
			}
			_ = os.Remove(ef.Path)
		}
	}

	// Record which unaddressed legacy events this consumer has now been shown,
	// so the next wait does not replay them.
	if result.Reason == "event" {
		reader.commitCursor(result.Events)
	}
	if awaitEventCleanup {
		reader.sweepLegacy(time.Now())
	}

	// Set effort level based on idle cycles.
	// context-yield forces full effort: context-check must not be abbreviated.
	if result.Reason == "event" || result.Reason == "context-yield" || result.IdleCycles == 0 {
		result.EffortLevel = "full"
	} else {
		result.EffortLevel = "abbreviated"
	}

	// Output
	if moleculeJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	if !awaitEventQuiet {
		switch result.Reason {
		case "event":
			fmt.Printf("%s %d event(s) received after %v\n",
				style.Bold.Render("✓"), len(result.Events), result.Elapsed.Round(time.Millisecond))
			for _, ef := range result.Events {
				// Show event type from content
				var parsed map[string]interface{}
				if json.Unmarshal(ef.Content, &parsed) == nil {
					if t, ok := parsed["type"].(string); ok {
						origin := ""
						if ef.Legacy {
							origin = style.Dim.Render(" (legacy layout)")
						}
						fmt.Printf("  %s %s%s\n", style.Dim.Render("→"), t, origin)
					}
				}
			}
			if notice := legacyNotice(reader.legacyDir, result.Events); notice != "" {
				fmt.Printf("%s %s\n", style.Dim.Render("⚠"), notice)
			}
		case "timeout":
			fmt.Printf("%s Timeout after %v (idle cycle: %d)\n",
				style.Dim.Render("⏱"), result.Elapsed.Round(time.Millisecond), result.IdleCycles)
		case "context-yield":
			fmt.Printf("%s Context-check interval reached after %v\n",
				style.Dim.Render("↺"), result.Elapsed.Round(time.Millisecond))
			fmt.Printf("\n%s Assess context usage before re-entering event wait.\n",
				style.Bold.Render("CONTEXT: check"))
			fmt.Printf("If context is OK, call await-event again. If context is high, hand off.\n")
		}

		// Output effort recommendation for the next patrol cycle.
		if result.EffortLevel == "abbreviated" {
			fmt.Printf("\n%s Run ABBREVIATED patrol: quick checks only, skip optional steps.\n",
				style.Bold.Render("EFFORT: reduced"))
		} else {
			fmt.Printf("\n%s Run full patrol.\n",
				style.Bold.Render("EFFORT: full"))
		}
	}

	return nil
}

// calculateEventTimeout mirrors calculateEffectiveTimeout for await-event.
func calculateEventTimeout(idleCycles int) (time.Duration, error) {
	if awaitEventBackoffBase != "" {
		base, err := time.ParseDuration(awaitEventBackoffBase)
		if err != nil {
			return 0, fmt.Errorf("invalid backoff-base: %w", err)
		}

		var maxDur time.Duration
		if awaitEventBackoffMax != "" {
			maxDur, err = time.ParseDuration(awaitEventBackoffMax)
			if err != nil {
				return 0, fmt.Errorf("invalid backoff-max: %w", err)
			}
		}

		timeout := base
		for i := 0; i < idleCycles; i++ {
			// Cap early to prevent int64 overflow at high idle counts.
			// time.Duration is int64 nanoseconds; multiplying repeatedly
			// without a guard wraps negative around idle ~62+ (30s base,
			// mult=2). Check before each multiply.
			if maxDur > 0 && timeout >= maxDur {
				return maxDur, nil
			}
			timeout *= time.Duration(awaitEventBackoffMult)
		}
		if maxDur > 0 && timeout > maxDur {
			return maxDur, nil
		}
		return timeout, nil
	}
	return time.ParseDuration(awaitEventTimeout)
}

// waitForEventFiles checks for pending events, then polls until events appear or timeout.
// Uses a polling loop instead of inotifywait for cross-platform compatibility.
//
// contextCheckAfter, when non-zero, causes an early return with reason "context-yield"
// after the given wall-clock duration. This allows the caller (a patrol agent) to
// assess context usage before re-entering the wait, preventing unbounded context
// accumulation during long idle periods.
func waitForEventFiles(ctx context.Context, eventDir string, contextCheckAfter time.Duration) (*AwaitEventResult, error) {
	return waitForChannelEvents(ctx, &channelReader{dir: eventDir}, contextCheckAfter)
}

// waitForChannelEvents is waitForEventFiles over a reader, which may cover the
// pre-rig-scoping directory in addition to the consumer's own one.
func waitForChannelEvents(ctx context.Context, reader *channelReader, contextCheckAfter time.Duration) (*AwaitEventResult, error) {
	// Check for already-pending events
	events, err := reader.read()
	if err != nil {
		return nil, err
	}
	if len(events) > 0 {
		return &AwaitEventResult{
			Reason: "event",
			Events: events,
		}, nil
	}

	// Calculate remaining timeout from context
	deadline, ok := ctx.Deadline()
	if !ok {
		return &AwaitEventResult{Reason: "timeout"}, nil
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return &AwaitEventResult{Reason: "timeout"}, nil
	}

	// Set up context-yield timer when requested.
	// A nil channel is never selected, so when contextCheckAfter is zero
	// the timer case never fires and existing behavior is preserved.
	var contextYieldC <-chan time.Time
	if contextCheckAfter > 0 {
		t := time.NewTimer(contextCheckAfter)
		defer t.Stop()
		contextYieldC = t.C
	}

	// Poll with 500ms interval until event appears or timeout.
	// This is cross-platform (no inotifywait dependency) and the 500ms
	// latency is acceptable for the event-driven patrol use case.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final check for events (race condition safety). Bound the
			// read so a stuck filesystem can't prevent us from returning —
			// the wait has already timed out, and reporting timeout is
			// more useful than hanging indefinitely on the last read.
			events = readPendingEventsBounded(ctx, reader, 500*time.Millisecond)
			if len(events) > 0 {
				return &AwaitEventResult{
					Reason: "event",
					Events: events,
				}, nil
			}
			return &AwaitEventResult{Reason: "timeout"}, nil
		case <-contextYieldC:
			// Context-check interval elapsed. Do a final event check before
			// yielding — if an event just arrived, return it instead.
			events = readPendingEventsBounded(ctx, reader, 500*time.Millisecond)
			if len(events) > 0 {
				return &AwaitEventResult{
					Reason: "event",
					Events: events,
				}, nil
			}
			return &AwaitEventResult{Reason: "context-yield"}, nil
		case <-ticker.C:
			// Run readPendingEvents in a goroutine so ctx.Done() can
			// always interrupt the wait. Without this, a slow/stuck
			// read (e.g., stalled filesystem, sleeping laptop) would
			// starve the timeout case until the read returns. This is
			// the root cause of gt-x2lc: the timeout deadline expired
			// but waitForEventFiles stayed blocked inside the read.
			type readRes struct {
				events []EventFile
				err    error
			}
			ch := make(chan readRes, 1)
			go func() {
				ev, er := reader.read()
				ch <- readRes{events: ev, err: er}
			}()
			select {
			case <-ctx.Done():
				// Timeout raced with read — abandon the goroutine and
				// let the outer loop's ctx.Done() case finalize.
				continue
			case res := <-ch:
				if res.err != nil {
					return nil, res.err
				}
				if len(res.events) > 0 {
					return &AwaitEventResult{
						Reason: "event",
						Events: res.events,
					}, nil
				}
			}
		}
	}
}

// readPendingEventsBounded runs readPendingEvents in a goroutine and returns
// whatever it produces within the given budget, or nil if it doesn't finish.
// ctx is also honored — whichever deadline fires first wins.
func readPendingEventsBounded(ctx context.Context, reader *channelReader, budget time.Duration) []EventFile {
	ch := make(chan []EventFile, 1)
	go func() {
		events, _ := reader.read()
		ch <- events
	}()
	select {
	case events := <-ch:
		return events
	case <-time.After(budget):
		return nil
	case <-ctx.Done():
		// ctx already done — give the read a tiny grace window so we
		// don't drop events that were 1ms from arriving.
		select {
		case events := <-ch:
			return events
		case <-time.After(50 * time.Millisecond):
			return nil
		}
	}
}

// readPendingEvents reads all .event files from the directory.
func readPendingEvents(dir string) ([]EventFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var events []EventFile
	var paths []string

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".event") {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}

	sort.Strings(paths) // oldest first

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue // skip unreadable files
		}
		ef := EventFile{
			Path:    path,
			Content: json.RawMessage(data),
		}
		if info, statErr := os.Stat(path); statErr == nil {
			ef.modTime = info.ModTime()
		}
		events = append(events, ef)
	}

	return events, nil
}

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/channelevents"
)

// Draining the pre-rig-scoping channel directory (gt-1qe).
//
// Rig scoping moved rig-scoped channels from <town>/events/<channel>/ to
// <town>/events/rigs/<rig>/<channel>/ (gt-em1). Both sides of the tree are in
// the same source release, but a town does not upgrade atomically: producers
// and consumers are separate long-lived processes running whichever binary they
// started with. A producer on an older binary therefore writes the flat
// directory that an upgraded consumer no longer watches, and the event is
// delivered to nobody — the merge still happens only because the refinery polls
// `gt mq list` on its patrol cycle, so the event pipeline contributes nothing
// and latency is a full cycle.
//
// So an upgraded consumer drains both directories for the migration window. The
// rule that makes that safe is that draining the flat directory must not
// re-create the crosstalk rig scoping exists to prevent:
//
//   - addressed to this rig    -> delivered, and deleted with --cleanup like any
//     other event of ours.
//   - addressed to another rig -> neither read nor deleted. Left where its
//     addressee will find it. This is the case that destroys events when a
//     consumer on the old layout eats it, and it is exactly what we refuse to do.
//   - addressed to nobody      -> written before producers stamped the rig, so no
//     consumer can tell whom it was for. Delivered to every rig's consumer at
//     most once each, tracked by a per-consumer cursor, and never deleted on
//     delivery: deleting it would starve the rig it was actually meant for. The
//     payload of these is a content-free "go look at your queue" wake-up, so an
//     extra wake costs a patrol cycle while a missed one costs the work.
//
// Unaddressed events are swept on age rather than on delivery, so the flat
// directory still drains to empty once producers have moved.

// legacyRetention is how long an unaddressed legacy event is kept before a
// consumer running --cleanup sweeps it. It has to outlast the slowest
// consumer's patrol cycle by a wide margin, since every consumer is entitled to
// see the event once and none of them coordinate.
const legacyRetention = 24 * time.Hour

// legacyCursorFile records, inside the consumer's own channel directory, the
// newest unaddressed legacy event it has already been shown. It lives beside
// the .event files and is not one of them.
const legacyCursorFile = ".legacy-cursor"

// channelReader is the set of event files one consumer is entitled to read: its
// own channel directory, plus — while the layout migration is in flight — the
// part of the pre-scoping flat directory that belongs to it.
type channelReader struct {
	dir       string    // the consumer's own channel directory
	legacyDir string    // pre-scoping flat directory, "" when there is none to drain
	rig       string    // the consumer's rig, "" for town-level channels
	cursor    time.Time // unaddressed legacy events at or before this were already delivered
}

// newChannelReader builds a reader for a resolved channel directory. rigName is
// empty for town-level channels, which have no legacy directory to drain —
// their directory never moved.
func newChannelReader(townRoot, eventDir, rigName, channel string) *channelReader {
	r := &channelReader{dir: eventDir, rig: rigName}
	if rigName != "" {
		r.legacyDir = channelevents.LegacyDir(townRoot, channel)
	}
	r.cursor = readLegacyCursor(eventDir)
	return r
}

// read returns every pending event this consumer may take, own directory first.
func (r *channelReader) read() ([]EventFile, error) {
	events, err := readPendingEvents(r.dir)
	if err != nil {
		return nil, err
	}
	legacy, err := r.readLegacy()
	if err != nil {
		// The consumer's own directory is the contract; a broken legacy
		// directory must not stop delivery from it.
		return events, nil
	}
	return append(events, legacy...), nil
}

// readLegacy returns the events in the flat directory that belong to this
// consumer, applying the addressing rules described at the top of this file.
func (r *channelReader) readLegacy() ([]EventFile, error) {
	if r.legacyDir == "" || r.rig == "" {
		return nil, nil
	}
	all, err := readPendingEvents(r.legacyDir)
	if err != nil || len(all) == 0 {
		return nil, err
	}

	var mine []EventFile
	for _, ef := range all {
		ef.Legacy = true
		switch rig := eventRig(ef.Content); {
		case rig == r.rig:
			// Ours by name: same semantics as an event in our own directory.
		case rig != "":
			// Addressed to another rig. Not ours to read, and emphatically
			// not ours to delete.
			continue
		default:
			// Unaddressed. Broadcast once to each consumer, never consumed.
			if !ef.modTime.After(r.cursor) {
				continue
			}
			ef.retain = true
		}
		mine = append(mine, ef)
	}
	return mine, nil
}

// commitCursor advances the cursor past the unaddressed legacy events in this
// delivery, so the next wait does not hand the consumer the same ones again.
// Committing from the delivered result rather than from the read keeps a read
// whose result was discarded (a bounded read that timed out) from skipping
// events that were never shown to anyone.
func (r *channelReader) commitCursor(events []EventFile) {
	if r.legacyDir == "" {
		return
	}
	newest := r.cursor
	for _, ef := range events {
		if ef.retain && ef.modTime.After(newest) {
			newest = ef.modTime
		}
	}
	if !newest.After(r.cursor) {
		return
	}
	if err := os.WriteFile(filepath.Join(r.dir, legacyCursorFile),
		[]byte(newest.Format(time.RFC3339Nano)), 0644); err != nil {
		return
	}
	r.cursor = newest
}

// sweepLegacy deletes unaddressed legacy events older than legacyRetention.
// They are never deleted on delivery — no consumer knows whose they were — so
// age is the only safe way the flat directory ever empties. Events addressed to
// a rig are left alone: their addressee is entitled to them however late it
// arrives. Returns how many it removed.
func (r *channelReader) sweepLegacy(now time.Time) int {
	if r.legacyDir == "" {
		return 0
	}
	all, err := readPendingEvents(r.legacyDir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, ef := range all {
		if eventRig(ef.Content) != "" {
			continue
		}
		if now.Sub(ef.modTime) < legacyRetention {
			continue
		}
		if os.Remove(ef.Path) == nil {
			removed++
		}
	}
	return removed
}

// eventRig reports which rig an event is addressed to: the top-level "rig"
// field written by the rig-scoped emitter, else payload.rig, else "" for an
// event from a producer that predates rig addressing.
func eventRig(content json.RawMessage) string {
	var parsed struct {
		Rig     string                 `json:"rig"`
		Payload map[string]interface{} `json:"payload"`
	}
	if err := json.Unmarshal(content, &parsed); err != nil {
		return ""
	}
	if rig := strings.TrimSpace(parsed.Rig); rig != "" {
		return rig
	}
	if rig, ok := parsed.Payload["rig"].(string); ok {
		return strings.TrimSpace(rig)
	}
	return ""
}

// readLegacyCursor loads the cursor, treating anything unreadable or unparsable
// as "nothing delivered yet". Re-showing an old wake-up is harmless; silently
// skipping one is not.
func readLegacyCursor(eventDir string) time.Time {
	data, err := os.ReadFile(filepath.Join(eventDir, legacyCursorFile))
	if err != nil {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}
	}
	return ts
}

// legacyNotice describes a delivery that came from the flat directory, for the
// operator: an upgraded consumer reading legacy events means some producer has
// not moved yet. Returns "" when nothing legacy was delivered.
func legacyNotice(legacyDir string, events []EventFile) string {
	count := 0
	for _, ef := range events {
		if ef.Legacy {
			count++
		}
	}
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("%d event(s) came from the pre-rig-scoping directory %s — "+
		"a producer is still on the old layout", count, legacyDir)
}

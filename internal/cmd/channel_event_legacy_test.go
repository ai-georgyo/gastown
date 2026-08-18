package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/channelevents"
)

// gt-1qe: a producer running a binary from before rig scoping writes the flat
// <town>/events/<channel>/ directory that an upgraded consumer no longer
// watches, so the event reaches nobody and delivery falls back to polling.
// These tests are written against the consumer's receipt, not against the file
// having been written — an assertion that a file appeared passes on the broken
// pairing, since the broken pairing writes the file just fine.

// writeLegacyEvent writes an event in the pre-rig-scoping directory, in the
// exact shape an old producer emits. rig is stamped only when non-empty: the
// binaries that stranded the events observed in the live town predate the rig
// field entirely, and that unaddressed case is the one with no safe consumer.
func writeLegacyEvent(t *testing.T, townRoot, channel, rig, message string, age time.Duration) string {
	t.Helper()

	dir := channelevents.LegacyDir(townRoot, channel)
	if dir == "" {
		t.Fatalf("channel %q has no legacy directory; the test would prove nothing", channel)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("creating legacy dir: %v", err)
	}

	event := map[string]interface{}{
		"type":      "MQ_SUBMIT",
		"channel":   channel,
		"timestamp": time.Now().Format(time.RFC3339),
		"payload":   map[string]string{"source": "sling", "message": message},
	}
	if rig != "" {
		event["rig"] = rig
		event["payload"].(map[string]string)["rig"] = rig
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshaling event: %v", err)
	}

	path := filepath.Join(dir, fmt.Sprintf("%d-legacy.event", time.Now().UnixNano()))
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("writing legacy event: %v", err)
	}
	if age > 0 {
		old := time.Now().Add(-age)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("ageing legacy event: %v", err)
		}
	}
	return path
}

// awaitAsRig waits the way a real rig-scoped consumer does: its own directory
// plus the legacy one.
func awaitAsRig(t *testing.T, townRoot, rig, channel string, timeout time.Duration) (*AwaitEventResult, *channelReader) {
	t.Helper()

	dir, err := channelevents.Dir(townRoot, rig, channel)
	if err != nil {
		t.Fatalf("resolving event dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("creating event dir: %v", err)
	}
	reader := newChannelReader(townRoot, dir, rig, channel)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	result, err := waitForChannelEvents(ctx, reader, 0)
	if err != nil {
		t.Fatalf("waitForChannelEvents failed: %v", err)
	}
	return result, reader
}

// reportLayout prints both directories with counts, so a failure says which
// side of the migration the events are sitting on rather than just "0 events".
func reportLayout(t *testing.T, townRoot, rig, channel string) {
	t.Helper()

	scoped, err := channelevents.Dir(townRoot, rig, channel)
	if err != nil {
		t.Fatalf("resolving event dir: %v", err)
	}
	for _, dir := range []string{scoped, channelevents.LegacyDir(townRoot, channel)} {
		names, err := filepath.Glob(filepath.Join(dir, "*.event"))
		if err != nil {
			t.Fatalf("listing %s: %v", dir, err)
		}
		sort.Strings(names)
		t.Logf("%-60s %d event(s)", dir, len(names))
		for _, n := range names {
			t.Logf("    %s", filepath.Base(n))
		}
	}
}

// The defect itself: an event written by an un-upgraded producer must still
// reach the upgraded consumer.
//
// POSITIVE CONTROL that this test can detect the defect: awaitOn() below waits
// on the rig-scoped directory alone, which is exactly what the consumer did
// before this change. It must time out on the same event that the draining
// consumer receives. If the drain regresses, the first assertion fails; if the
// control ever stops timing out, the test has stopped testing anything.
func TestLegacyChannel_UnaddressedEventReachesUpgradedConsumer(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()

	writeLegacyEvent(t, townRoot, "refinery", "", "MERGE_READY received - check inbox for pending work", 0)
	reportLayout(t, townRoot, "gastown", "refinery")

	control := awaitOn(t, townRoot, "gastown", "refinery", 1500*time.Millisecond)
	if control.Reason != "timeout" {
		t.Fatalf("control: the rig-scoped directory alone saw the legacy event (reason=%q); "+
			"the test can no longer detect the defect", control.Reason)
	}

	result, _ := awaitAsRig(t, townRoot, "gastown", "refinery", 2*time.Second)
	if result.Reason != "event" || len(result.Events) != 1 {
		t.Fatalf("upgraded consumer did not receive the legacy event: reason=%q events=%d",
			result.Reason, len(result.Events))
	}
	if !result.Events[0].Legacy {
		t.Error("event delivered from the legacy directory is not marked Legacy")
	}
}

// The mayor's case, and the one that destroys events: a consumer must not take
// an event addressed to another rig, even out of the shared legacy directory.
func TestLegacyChannel_AddressedEventIsNotTakenByAnotherRig(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()

	path := writeLegacyEvent(t, townRoot, "refinery", "gastown", "MERGE_READY received", 0)

	other, _ := awaitAsRig(t, townRoot, "nix_conf", "refinery", 1500*time.Millisecond)
	if other.Reason != "timeout" {
		reportLayout(t, townRoot, "nix_conf", "refinery")
		t.Fatalf("nix_conf's consumer took an event addressed to gastown: reason=%q events=%d",
			other.Reason, len(other.Events))
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("gastown's event was removed by another rig's consumer: %v", err)
	}

	mine, _ := awaitAsRig(t, townRoot, "gastown", "refinery", 2*time.Second)
	if mine.Reason != "event" || len(mine.Events) != 1 {
		t.Fatalf("gastown did not receive its own legacy event: reason=%q events=%d",
			mine.Reason, len(mine.Events))
	}
	if mine.Events[0].retain {
		t.Error("an event addressed to this rig should be consumable, not retained")
	}
}

// An unaddressed legacy event belongs to a rig nobody can name, so it is shown
// to each consumer once and consumed by none of them.
func TestLegacyChannel_UnaddressedEventIsBroadcastOnceAndNotConsumed(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()

	path := writeLegacyEvent(t, townRoot, "refinery", "", "MERGE_READY received", 0)

	first, reader := awaitAsRig(t, townRoot, "gastown", "refinery", 2*time.Second)
	if first.Reason != "event" || len(first.Events) != 1 {
		t.Fatalf("gastown did not receive the unaddressed event: reason=%q", first.Reason)
	}
	if !first.Events[0].retain {
		t.Fatal("an unaddressed legacy event must be retained: deleting it starves the rig it was meant for")
	}
	reader.commitCursor(first.Events)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("unaddressed legacy event was deleted on delivery: %v", err)
	}

	// A second rig is still entitled to see it.
	second, _ := awaitAsRig(t, townRoot, "nix_conf", "refinery", 2*time.Second)
	if second.Reason != "event" || len(second.Events) != 1 {
		t.Errorf("nix_conf did not see the unaddressed event: reason=%q", second.Reason)
	}

	// The first rig must not be handed it again — otherwise the consumer spins
	// on a file that is never deleted.
	repeat, _ := awaitAsRig(t, townRoot, "gastown", "refinery", 1500*time.Millisecond)
	if repeat.Reason != "timeout" {
		t.Errorf("gastown was handed the same unaddressed event twice: reason=%q events=%d",
			repeat.Reason, len(repeat.Events))
	}
}

// Age is the only thing that empties the flat directory of events nobody can
// claim, so it has to actually work — and it must not touch an event that is
// still addressed to a rig.
func TestLegacyChannel_SweepRemovesOnlyExpiredUnaddressedEvents(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()

	expired := writeLegacyEvent(t, townRoot, "refinery", "", "old wake-up", legacyRetention+time.Hour)
	fresh := writeLegacyEvent(t, townRoot, "refinery", "", "recent wake-up", 0)
	addressed := writeLegacyEvent(t, townRoot, "refinery", "nix_conf", "someone else's", legacyRetention+time.Hour)

	dir, err := channelevents.Dir(townRoot, "gastown", "refinery")
	if err != nil {
		t.Fatalf("resolving event dir: %v", err)
	}
	reader := newChannelReader(townRoot, dir, "gastown", "refinery")

	if removed := reader.sweepLegacy(time.Now()); removed != 1 {
		t.Errorf("sweep removed %d event(s), want 1", removed)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Error("expired unaddressed event survived the sweep")
	}
	for _, keep := range []string{fresh, addressed} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("sweep removed %s: %v", filepath.Base(keep), err)
		}
	}
}

// Town-level channels never moved, so their directory is live, not legacy.
// Draining it as if it were legacy would give the mayor's own events the
// broadcast semantics and stop --cleanup consuming them.
func TestLegacyChannel_TownLevelChannelHasNoLegacyDirectory(t *testing.T) {
	t.Parallel()

	if dir := channelevents.LegacyDir(t.TempDir(), "mayor"); dir != "" {
		t.Errorf("town-level channel reported a legacy directory: %q", dir)
	}
}

// The producer half of the contract: emitting a rig-scoped channel without a
// rig used to silently write the flat directory, which is the same misdelivery
// seen from the other side.
func TestLegacyChannel_RigScopedEmitRequiresARig(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	channelevents.AllowEmitForTest(t, townRoot)

	if _, err := channelevents.EmitToTown(townRoot, "refinery", "MQ_SUBMIT", nil); err == nil {
		t.Error("emitting the rig-scoped refinery channel with no rig was allowed")
	}
	if entries, err := filepath.Glob(filepath.Join(townRoot, "events", "refinery", "*.event")); err == nil && len(entries) > 0 {
		t.Errorf("a rig-less emit wrote %d event(s) to the flat directory", len(entries))
	}
	// The town-level channels must keep working.
	if _, err := channelevents.EmitToTown(townRoot, "mayor", "SLOT_OPEN", []string{"rig=gastown"}); err != nil {
		t.Errorf("town-level emit broke: %v", err)
	}
}

// End to end through the real producer: gt mq submit / gt done call
// nudgeRefinery, so this is the code path that emitted the stranded events.
// Asserted on the consumer's receipt, and on the other rig's silence.
func TestLegacyChannel_SlingEmitReachesItsOwnRigOnly(t *testing.T) {
	townRoot := newFakeTown(t) // chdir: nudgeRefinery resolves the town from cwd
	channelevents.AllowEmitForTest(t, townRoot)
	t.Setenv("GT_TEST_NUDGE_LOG", "") // take the real path, not the log stub

	nudgeRefinery("gastown", "MERGE_READY received - check inbox for pending work")
	reportLayout(t, townRoot, "gastown", "refinery")

	delivered, _ := awaitAsRig(t, townRoot, "gastown", "refinery", 2*time.Second)
	if delivered.Reason != "event" || len(delivered.Events) != 1 {
		t.Fatalf("gastown's refinery did not receive the submit it was sent: reason=%q events=%d",
			delivered.Reason, len(delivered.Events))
	}
	if delivered.Events[0].Legacy {
		t.Error("a current producer wrote the legacy directory")
	}

	other, _ := awaitAsRig(t, townRoot, "nix_conf", "refinery", 1500*time.Millisecond)
	if other.Reason != "timeout" {
		t.Errorf("nix_conf's refinery woke for gastown's submit: reason=%q events=%d",
			other.Reason, len(other.Events))
	}
}

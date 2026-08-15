package cmd

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/channelevents"
)

// These tests cover gt-em1: the refinery event channel used to be one
// town-global directory, so every rig's refinery woke for every other rig's
// MQ_SUBMIT — and, because the channel is single-consumer with --cleanup, the
// wrong refinery could consume an event addressed to another rig.

func awaitOn(t *testing.T, townRoot, rigName, channel string, timeout time.Duration) *AwaitEventResult {
	t.Helper()
	dir, err := channelevents.Dir(townRoot, rigName, channel)
	if err != nil {
		t.Fatalf("resolving event dir: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	result, err := waitForEventFiles(ctx, dir, 0)
	if err != nil {
		t.Fatalf("waitForEventFiles failed: %v", err)
	}
	return result
}

func eventPayload(t *testing.T, ef EventFile) map[string]interface{} {
	t.Helper()
	var parsed struct {
		Payload map[string]interface{} `json:"payload"`
	}
	if err := json.Unmarshal(ef.Content, &parsed); err != nil {
		t.Fatalf("parsing event content: %v", err)
	}
	return parsed.Payload
}

// Test 1: a submit on rig A does not wake rig B's refinery.
func TestChannelEvents_SubmitOnOneRigDoesNotWakeAnother(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	channelevents.AllowEmitForTest(t, townRoot)

	if _, err := channelevents.EmitToRig(townRoot, "gastown", "refinery", "MQ_SUBMIT", []string{"rig=gastown"}); err != nil {
		t.Fatalf("emit failed: %v", err)
	}

	result := awaitOn(t, townRoot, "avalon", "refinery", 1500*time.Millisecond)
	if result.Reason != "timeout" {
		t.Errorf("avalon's refinery woke for gastown's submit: reason=%q events=%d",
			result.Reason, len(result.Events))
	}
}

// Test 2: a submit on rig A is still delivered to rig A's refinery, and is not
// consumable by rig B — even after A consumes it with --cleanup semantics.
func TestChannelEvents_SubmitIsDeliveredToOwningRigOnly(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	channelevents.AllowEmitForTest(t, townRoot)

	if _, err := channelevents.EmitToRig(townRoot, "gastown", "refinery", "MQ_SUBMIT", []string{"rig=gastown"}); err != nil {
		t.Fatalf("emit failed: %v", err)
	}

	delivered := awaitOn(t, townRoot, "gastown", "refinery", 2*time.Second)
	if delivered.Reason != "event" || len(delivered.Events) != 1 {
		t.Fatalf("gastown's refinery did not receive its own submit: reason=%q events=%d",
			delivered.Reason, len(delivered.Events))
	}
	if got := eventPayload(t, delivered.Events[0])["rig"]; got != "gastown" {
		t.Errorf("payload.rig = %v, want gastown", got)
	}

	// --cleanup deletes the file after reading; that must only affect the
	// owning rig's directory.
	if err := os.Remove(delivered.Events[0].Path); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	other := awaitOn(t, townRoot, "avalon", "refinery", 500*time.Millisecond)
	if other.Reason != "timeout" {
		t.Errorf("avalon consumed an event addressed to gastown: reason=%q", other.Reason)
	}
}

// Test 3: concurrent submits on two rigs each reach the correct refinery.
func TestChannelEvents_ConcurrentSubmitsReachCorrectRig(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	channelevents.AllowEmitForTest(t, townRoot)

	results := make(map[string]*AwaitEventResult, 2)
	var mu sync.Mutex
	var waiters sync.WaitGroup

	for _, rigName := range []string{"gastown", "avalon"} {
		waiters.Add(1)
		go func() {
			defer waiters.Done()
			result := awaitOn(t, townRoot, rigName, "refinery", 5*time.Second)
			mu.Lock()
			defer mu.Unlock()
			results[rigName] = result
		}()
	}

	var emitters sync.WaitGroup
	for _, rigName := range []string{"gastown", "avalon"} {
		emitters.Add(1)
		go func() {
			defer emitters.Done()
			if _, err := channelevents.EmitToRig(townRoot, rigName, "refinery", "MQ_SUBMIT", []string{"rig=" + rigName}); err != nil {
				t.Errorf("emit for %s failed: %v", rigName, err)
			}
		}()
	}
	emitters.Wait()
	waiters.Wait()

	for _, rigName := range []string{"gastown", "avalon"} {
		result := results[rigName]
		if result == nil || result.Reason != "event" {
			t.Errorf("%s's refinery did not receive an event: %+v", rigName, result)
			continue
		}
		if len(result.Events) != 1 {
			t.Errorf("%s's refinery received %d events, want exactly its own", rigName, len(result.Events))
			continue
		}
		if got := eventPayload(t, result.Events[0])["rig"]; got != rigName {
			t.Errorf("%s's refinery received an event for rig %v", rigName, got)
		}
	}
}

// Town-level channels keep sharing one directory: the mayor is the single
// consumer for the whole town, so rig scoping would break delivery.
func TestChannelEvents_TownLevelChannelStaysShared(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	channelevents.AllowEmitForTest(t, townRoot)

	if _, err := channelevents.EmitToTown(townRoot, "mayor", "SLOT_OPEN", []string{"rig=gastown"}); err != nil {
		t.Fatalf("emit failed: %v", err)
	}

	result := awaitOn(t, townRoot, "", "mayor", 2*time.Second)
	if result.Reason != "event" || len(result.Events) != 1 {
		t.Fatalf("mayor did not receive SLOT_OPEN: reason=%q events=%d", result.Reason, len(result.Events))
	}
}

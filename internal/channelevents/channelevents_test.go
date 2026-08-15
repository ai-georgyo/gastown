package channelevents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmitToTown(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	AllowEmitForTest(t, townRoot)

	path, err := EmitToTown(townRoot, "refinery", "MERGE_READY", []string{
		"source=witness",
		"rig=dashboard",
	})
	if err != nil {
		t.Fatalf("EmitToTown failed: %v", err)
	}

	if !strings.HasSuffix(path, ".event") {
		t.Errorf("expected .event suffix, got %q", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading event file: %v", err)
	}

	var event map[string]interface{}
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("unmarshaling event: %v", err)
	}

	if event["type"] != "MERGE_READY" {
		t.Errorf("type = %v, want MERGE_READY", event["type"])
	}
	if event["channel"] != "refinery" {
		t.Errorf("channel = %v, want refinery", event["channel"])
	}

	payload, ok := event["payload"].(map[string]interface{})
	if !ok {
		t.Fatal("payload is not a map")
	}
	if payload["source"] != "witness" {
		t.Errorf("payload.source = %v, want witness", payload["source"])
	}
	if payload["rig"] != "dashboard" {
		t.Errorf("payload.rig = %v, want dashboard", payload["rig"])
	}
}

func TestEmitToTown_InvalidChannel(t *testing.T) {
	t.Parallel()
	_, err := EmitToTown(t.TempDir(), "../escape", "TEST", nil)
	if err == nil {
		t.Error("expected error for invalid channel name")
	}
}

func TestEmitToTown_UniqueFilenames(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	AllowEmitForTest(t, townRoot)
	seen := make(map[string]bool)

	for i := 0; i < 10; i++ {
		path, err := EmitToTown(townRoot, "test", "EVENT", nil)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if seen[path] {
			t.Errorf("duplicate filename: %s", path)
		}
		seen[path] = true
	}
}

func TestValidChannelName(t *testing.T) {
	t.Parallel()
	valid := []string{"refinery", "witness", "my-channel", "test_chan", "abc123"}
	for _, name := range valid {
		if !ValidChannelName.MatchString(name) {
			t.Errorf("%q should be valid", name)
		}
	}

	invalid := []string{"../escape", "has space", "has/slash", "", "has.dot"}
	for _, name := range invalid {
		if ValidChannelName.MatchString(name) {
			t.Errorf("%q should be invalid", name)
		}
	}
}

func TestEmitToTown_CreatesDirectory(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	AllowEmitForTest(t, townRoot)
	channelDir := filepath.Join(townRoot, "events", "newchannel")

	if _, err := os.Stat(channelDir); !os.IsNotExist(err) {
		t.Fatal("channel dir should not exist yet")
	}

	_, err := EmitToTown(townRoot, "newchannel", "TEST", nil)
	if err != nil {
		t.Fatalf("EmitToTown failed: %v", err)
	}

	if _, err := os.Stat(channelDir); err != nil {
		t.Errorf("channel dir should exist after emit: %v", err)
	}
}

func TestIsRigScoped(t *testing.T) {
	t.Parallel()
	for _, ch := range []string{"refinery", "witness"} {
		if !IsRigScoped(ch) {
			t.Errorf("%q should be rig-scoped", ch)
		}
	}
	for _, ch := range []string{"mayor", "deacon", "my-channel", ""} {
		if IsRigScoped(ch) {
			t.Errorf("%q should be town-level", ch)
		}
	}
}

func TestDir(t *testing.T) {
	t.Parallel()
	townRoot := filepath.Join("/tmp", "town")

	townDir, err := Dir(townRoot, "", "mayor")
	if err != nil {
		t.Fatalf("Dir(town-level) failed: %v", err)
	}
	if want := filepath.Join(townRoot, "events", "mayor"); townDir != want {
		t.Errorf("town dir = %q, want %q", townDir, want)
	}

	rigDir, err := Dir(townRoot, "gastown", "refinery")
	if err != nil {
		t.Fatalf("Dir(rig-scoped) failed: %v", err)
	}
	if want := filepath.Join(townRoot, "events", "rigs", "gastown", "refinery"); rigDir != want {
		t.Errorf("rig dir = %q, want %q", rigDir, want)
	}
}

func TestDir_RejectsPathTraversal(t *testing.T) {
	t.Parallel()
	if _, err := Dir("/tmp/town", "../../etc", "refinery"); err == nil {
		t.Error("expected error for rig name with path traversal")
	}
	if _, err := Dir("/tmp/town", "has/slash", "refinery"); err == nil {
		t.Error("expected error for rig name with slash")
	}
	if _, err := Dir("/tmp/town", "gastown", "../escape"); err == nil {
		t.Error("expected error for channel name with path traversal")
	}
}

// TestEmitToRig_IsolatesRigs is the core of gt-em1: an event emitted for one
// rig must not land anywhere another rig's agent is watching.
func TestEmitToRig_IsolatesRigs(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	AllowEmitForTest(t, townRoot)

	path, err := EmitToRig(townRoot, "gastown", "refinery", "MQ_SUBMIT", []string{"source=sling"})
	if err != nil {
		t.Fatalf("EmitToRig failed: %v", err)
	}

	gastownDir, _ := Dir(townRoot, "gastown", "refinery")
	if filepath.Dir(path) != gastownDir {
		t.Errorf("event written to %q, want it under %q", filepath.Dir(path), gastownDir)
	}

	// Neither the other rig's directory nor the town-level one sees it.
	avalonDir, _ := Dir(townRoot, "avalon", "refinery")
	if entries, err := os.ReadDir(avalonDir); err == nil && len(entries) > 0 {
		t.Errorf("avalon's refinery dir should be empty, has %d entries", len(entries))
	}
	sharedDir, _ := Dir(townRoot, "", "refinery")
	if entries, err := os.ReadDir(sharedDir); err == nil && len(entries) > 0 {
		t.Errorf("town-level refinery dir should be empty, has %d entries", len(entries))
	}
}

func TestEmitToRig_StampsRig(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	AllowEmitForTest(t, townRoot)

	path, err := EmitToRig(townRoot, "gastown", "refinery", "MQ_SUBMIT", nil)
	if err != nil {
		t.Fatalf("EmitToRig failed: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading event file: %v", err)
	}
	var event map[string]interface{}
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("unmarshaling event: %v", err)
	}
	if event["rig"] != "gastown" {
		t.Errorf("rig = %v, want gastown", event["rig"])
	}
}

func TestEmitToTown_OmitsRigField(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	AllowEmitForTest(t, townRoot)

	path, err := EmitToTown(townRoot, "mayor", "SLOT_OPEN", nil)
	if err != nil {
		t.Fatalf("EmitToTown failed: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading event file: %v", err)
	}
	var event map[string]interface{}
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("unmarshaling event: %v", err)
	}
	if _, ok := event["rig"]; ok {
		t.Errorf("town-level event should not carry a rig field, got %v", event["rig"])
	}
}

func TestEmitToRig_InvalidRig(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	AllowEmitForTest(t, townRoot)
	if _, err := EmitToRig(townRoot, "../escape", "refinery", "TEST", nil); err == nil {
		t.Error("expected error for invalid rig name")
	}
}

func TestDir_RejectsReservedRigsChannel(t *testing.T) {
	t.Parallel()
	// A town-level channel named "rigs" would sit on top of the per-rig tree.
	if _, err := Dir("/tmp/town", "", "rigs"); err == nil {
		t.Error("expected error for the reserved channel name \"rigs\"")
	}
	reserved := t.TempDir()
	AllowEmitForTest(t, reserved)
	if _, err := EmitToTown(reserved, "rigs", "TEST", nil); err == nil {
		t.Error("expected EmitToTown to reject the reserved channel name")
	}
}

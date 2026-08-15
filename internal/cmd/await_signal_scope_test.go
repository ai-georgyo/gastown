package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestTown creates a town root containing the named rigs (a rig is a
// directory with a config.json, the same marker detectRigFromPath uses).
func newTestTown(t *testing.T, rigs ...string) string {
	t.Helper()
	townRoot := t.TempDir()
	for _, name := range rigs {
		dir := filepath.Join(townRoot, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir rig %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o644); err != nil {
			t.Fatalf("write rig config %s: %v", name, err)
		}
	}
	// Non-rig town directories must not be mistaken for rigs.
	for _, name := range []string{"mayor", "deacon", ".beads"} {
		if err := os.MkdirAll(filepath.Join(townRoot, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	return townRoot
}

func TestRigScopeMatcher(t *testing.T) {
	townRoot := newTestTown(t, "gastown", "avalon", "ifconfigio")

	tests := []struct {
		name string
		line string
		want bool
	}{
		// --- mine: wake ---
		{
			name: "own polecat done",
			line: `{"ts":"t","type":"done","actor":"gastown/polecats/capable","payload":{"bead":"gt-em1","branch":"b"}}`,
			want: true,
		},
		{
			name: "mail from the mayor to my rig",
			line: `{"ts":"t","type":"mail","actor":"mayor/","payload":{"subject":"s","to":"gastown/refinery"}}`,
			want: true,
		},
		{
			name: "sling onto my rig's polecat",
			line: `{"ts":"t","type":"sling","actor":"mayor","payload":{"bead":"gt-qc1","target":"gastown/polecats/valkyrie"}}`,
			want: true,
		},
		{
			name: "spawn carries the rig in the payload, actor is bare gt",
			line: `{"ts":"t","type":"spawn","actor":"gt","payload":{"polecat":"valkyrie","rig":"gastown"}}`,
			want: true,
		},
		{
			name: "session death of my polecat, actor is a tmux session name",
			line: `{"ts":"t","type":"session_death","actor":"gt-valkyrie","payload":{"agent":"gastown/polecats/valkyrie","reason":"crash"}}`,
			want: true,
		},

		// --- another rig's: skip ---
		{
			name: "another rig's refinery mails the mayor",
			line: `{"ts":"t","type":"mail","actor":"ifconfigio/refinery","payload":{"subject":"s","to":"mayor/"}}`,
			want: false,
		},
		{
			name: "mayor mails another rig",
			line: `{"ts":"t","type":"mail","actor":"mayor/","payload":{"subject":"s","to":"avalon/refinery"}}`,
			want: false,
		},
		{
			name: "another rig's nudge carries its rig in the payload",
			line: `{"ts":"t","type":"nudge","actor":"avalon/witness","payload":{"rig":"avalon","target":"avalon/refinery","reason":"r"}}`,
			want: false,
		},
		{
			name: "stale rig no longer on disk is still not mine",
			line: `{"ts":"t","type":"session_death","actor":"gt-mycat","payload":{"agent":"myr/polecats/mycat","reason":"idle-reap"}}`,
			want: false,
		},

		// --- nobody's in particular: wake ---
		{
			name: "town halt names no rig",
			line: `{"ts":"t","type":"halt","actor":"gt","payload":{"services":["daemon"]}}`,
			want: true,
		},
		{
			name: "mass death names no rig",
			line: `{"ts":"t","type":"mass_death","actor":"daemon","payload":{"count":3,"window":"5s"}}`,
			want: true,
		},
		{
			name: "deacon nudges boot",
			line: `{"ts":"t","type":"nudge","actor":"deacon","payload":{"rig":"","target":"hq-boot","reason":"r"}}`,
			want: true,
		},
		{
			name: "escalation puts a bead ID in the rig field",
			line: `{"ts":"t","type":"escalation_sent","actor":"deacon/","payload":{"rig":"hq-wisp-g0ei","target":"deacon/","to":"mayor"}}`,
			want: true,
		},
		{
			name: "unparseable line wakes rather than being swallowed",
			line: `not json at all`,
			want: true,
		},
		{
			name: "convoy is a town-level address prefix",
			line: `{"ts":"t","type":"mail","actor":"convoy/hq-cv-l7v6k","payload":{"subject":"Convoy complete","to":"mayor/"}}`,
			want: true,
		},
	}

	m := newRigScopeMatcher(townRoot, "gastown")
	if m == nil {
		t.Fatal("newRigScopeMatcher returned nil for a non-empty rig")
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := m.matches(tt.line); got != tt.want {
				t.Errorf("matches(%s) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestRigScopeMatcherNilMatchesEverything(t *testing.T) {
	// No --rig means town-wide, the behavior town-level agents (deacon) need.
	if newRigScopeMatcher(t.TempDir(), "") != nil {
		t.Fatal("expected nil matcher when no rig scope is requested")
	}

	var m *rigScopeMatcher
	for _, line := range []string{
		`{"ts":"t","type":"mail","actor":"avalon/refinery","payload":{"to":"mayor/"}}`,
		`{"ts":"t","type":"halt","actor":"gt"}`,
		`garbage`,
	} {
		if !m.matches(line) {
			t.Errorf("nil matcher rejected %q", line)
		}
	}
}

func TestDiscoverRigNames(t *testing.T) {
	townRoot := newTestTown(t, "gastown", "avalon")
	rigs := discoverRigNames(townRoot)

	for _, want := range []string{"gastown", "avalon"} {
		if _, ok := rigs[want]; !ok {
			t.Errorf("expected rig %q to be discovered, got %v", want, rigs)
		}
	}
	for _, notRig := range []string{"mayor", "deacon", ".beads"} {
		if _, ok := rigs[notRig]; ok {
			t.Errorf("%q is not a rig but was discovered", notRig)
		}
	}
}

func TestWaitForEventsFile_SkipsOtherRigsUntilTimeout(t *testing.T) {
	// The bug this fixes: another rig's traffic returned "signal" immediately,
	// so the idle counter never advanced and backoff never engaged.
	townRoot := newTestTown(t, "gastown", "avalon")
	eventsPath := filepath.Join(townRoot, ".events.jsonl")
	if err := os.WriteFile(eventsPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	go func() {
		time.Sleep(200 * time.Millisecond)
		f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		for i := 0; i < 3; i++ {
			_, _ = f.WriteString(`{"ts":"t","type":"mail","actor":"avalon/refinery","payload":{"to":"mayor/"}}` + "\n")
			time.Sleep(100 * time.Millisecond)
		}
	}()

	result, err := waitForEventsFile(ctx, eventsPath, newRigScopeMatcher(townRoot, "gastown"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Reason != "timeout" {
		t.Errorf("expected reason 'timeout' (other rigs must not wake us), got %q with signal %q",
			result.Reason, result.Signal)
	}
	if result.Filtered != 3 {
		t.Errorf("expected 3 filtered events, got %d", result.Filtered)
	}
}

func TestWaitForEventsFile_WakesOnOwnRigAfterSkipping(t *testing.T) {
	townRoot := newTestTown(t, "gastown", "avalon")
	eventsPath := filepath.Join(townRoot, ".events.jsonl")
	if err := os.WriteFile(eventsPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		time.Sleep(200 * time.Millisecond)
		f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		// A burst: two foreign events then one of mine, all in one flush.
		_, _ = f.WriteString(
			`{"ts":"t","type":"mail","actor":"avalon/refinery","payload":{"to":"mayor/"}}` + "\n" +
				`{"ts":"t","type":"mail","actor":"avalon/witness","payload":{"to":"mayor/"}}` + "\n" +
				`{"ts":"t","type":"sling","actor":"mayor","payload":{"target":"gastown/polecats/valkyrie"}}` + "\n")
	}()

	result, err := waitForEventsFile(ctx, eventsPath, newRigScopeMatcher(townRoot, "gastown"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Reason != "signal" {
		t.Fatalf("expected reason 'signal', got %q", result.Reason)
	}
	if result.Filtered != 2 {
		t.Errorf("expected 2 filtered events before the match, got %d", result.Filtered)
	}
	if want := "gastown/polecats/valkyrie"; !strings.Contains(result.Signal, want) {
		t.Errorf("expected the signal line to be my rig's event, got %q", result.Signal)
	}
}

func TestWaitForEventsFile_PartialLineIsNotLost(t *testing.T) {
	// A writer that has not yet flushed its newline must not cost us the
	// event: ReadString consumes the partial line along with io.EOF.
	eventsPath := filepath.Join(t.TempDir(), ".events.jsonl")
	if err := os.WriteFile(eventsPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		time.Sleep(200 * time.Millisecond)
		_, _ = f.WriteString(`{"ts":"t","type":"sling","actor":"gast`)
		time.Sleep(500 * time.Millisecond)
		_, _ = f.WriteString(`own/witness"}` + "\n")
	}()

	result, err := waitForEventsFile(ctx, eventsPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Reason != "signal" {
		t.Fatalf("expected reason 'signal', got %q", result.Reason)
	}
	if want := `{"ts":"t","type":"sling","actor":"gastown/witness"}`; result.Signal != want {
		t.Errorf("signal = %q, want the reassembled line %q", result.Signal, want)
	}
}

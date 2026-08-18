package tmux

import (
	"strings"
	"testing"
)

// TestIsGTBindingCurrent_DetectsStalePattern verifies that isGTBindingCurrent
// returns false when the baked-in pattern doesn't match the current pattern.
// This is the core of the gt rig add fix: after adding a rig, the prefix
// pattern changes and existing bindings become stale.
func TestIsGTBindingCurrent_DetectsStalePattern(t *testing.T) {
	tm := newTestTmux(t)

	session := "gt-test-stale-" + t.Name()
	_ = tm.KillSession(session)
	defer func() { _ = tm.KillSession(session) }()
	defer restoreCycleKeyDefaults(tm)

	if err := tm.NewSessionWithCommand(session, "", "sleep 30"); err != nil {
		t.Fatalf("session creation: %v", err)
	}

	// Install a binding with an OLD pattern (missing a hypothetical "qu" prefix)
	oldPattern := "^(gt|hq)-"
	oldIfShell := "echo '#{session_name}' | grep -Eq '" + oldPattern + "'"
	if _, err := tm.run("bind-key", "-T", "prefix", "n",
		"if-shell", oldIfShell,
		"run-shell 'gt cycle next --session #{session_name} --client #{client_tty}'",
		"next-window"); err != nil {
		t.Fatalf("installing old binding: %v", err)
	}

	// Verify the binding has --client (so isGTBindingWithClient returns true)
	if !tm.isGTBindingWithClient("prefix", "n") {
		t.Fatal("expected isGTBindingWithClient to return true for the installed binding")
	}

	// But the pattern is stale — a new pattern with "qu" should not match
	newPattern := "^(gt|hq|qu)-"
	if tm.isGTBindingCurrent("prefix", "n", newPattern) {
		t.Error("expected isGTBindingCurrent to return false for stale pattern")
	}

	// The old pattern should still match
	if !tm.isGTBindingCurrent("prefix", "n", oldPattern) {
		t.Error("expected isGTBindingCurrent to return true for matching pattern")
	}
}

// TestSetCycleBindings_RefreshesStalePattern verifies that SetCycleBindings
// re-binds when the existing binding has a stale prefix pattern, even though
// it already has --client support.
func TestSetCycleBindings_RefreshesStalePattern(t *testing.T) {
	tm := newTestTmux(t)

	session := "gt-test-refresh-" + t.Name()
	_ = tm.KillSession(session)
	defer func() { _ = tm.KillSession(session) }()
	defer restoreCycleKeyDefaults(tm)

	if err := tm.NewSessionWithCommand(session, "", "sleep 30"); err != nil {
		t.Fatalf("session creation: %v", err)
	}

	// Install a binding with a STALE pattern (only gt|hq, missing other prefixes)
	stalePattern := "^(gt|hq)-"
	staleIfShell := "echo '#{session_name}' | grep -Eq '" + stalePattern + "'"
	if _, err := tm.run("bind-key", "-T", "prefix", "n",
		"if-shell", staleIfShell,
		"run-shell 'gt cycle next --session #{session_name} --client #{client_tty}'",
		"next-window"); err != nil {
		t.Fatalf("installing stale binding: %v", err)
	}
	if _, err := tm.run("bind-key", "-T", "prefix", "p",
		"if-shell", staleIfShell,
		"run-shell 'gt cycle prev --session #{session_name} --client #{client_tty}'",
		"previous-window"); err != nil {
		t.Fatalf("installing stale binding for p: %v", err)
	}

	// Call SetCycleBindings — it should detect the stale pattern and re-bind
	if err := tm.SetCycleBindings(session); err != nil {
		t.Fatalf("SetCycleBindings: %v", err)
	}

	// Verify the binding was updated with the current pattern
	currentPattern := sessionPrefixPattern()
	output, _ := tm.keyBinding("prefix", "n")
	if output == "" {
		t.Fatal("prefix-n has no binding after SetCycleBindings")
	}
	if !strings.Contains(output, currentPattern) {
		t.Errorf("expected binding to contain current pattern %q, got: %s", currentPattern, output)
	}
}

// restoreCycleKeyDefaults puts prefix n/p back to their stock tmux commands.
// Tests in this package share one tmux server (see TestMain), so a test that
// installs a GT cycle binding would otherwise leak it into later tests.
func restoreCycleKeyDefaults(tm *Tmux) {
	_, _ = tm.run("bind-key", "-T", "prefix", "n", "next-window")
	_, _ = tm.run("bind-key", "-T", "prefix", "p", "previous-window")
}

// TestSplitBindingLine covers the `tmux list-keys` line parser that backs
// keyBinding(). It exists because tmux 3.7 stopped honoring the single-key
// form `list-keys -T <table> <key>` (it exits 0 with no output even when the
// key is bound), so every binding probe now parses the whole-table listing
// itself. These cases are real 3.7b output (gt-1su).
func TestSplitBindingLine(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		table       string
		wantKey     string
		wantCommand string
		wantOK      bool
	}{
		{
			name:        "plain binding",
			line:        "bind-key    -T prefix n       next-window",
			table:       "prefix",
			wantKey:     "n",
			wantCommand: "next-window",
			wantOK:      true,
		},
		{
			name:        "repeat flag before -T",
			line:        "bind-key -r -T prefix Up      select-pane -U",
			table:       "prefix",
			wantKey:     "Up",
			wantCommand: "select-pane -U",
			wantOK:      true,
		},
		{
			name:        "command with arguments keeps internal spacing",
			line:        `bind-key    -T prefix $       command-prompt -I "#S" { rename-session "%%" }`,
			table:       "prefix",
			wantKey:     "$",
			wantCommand: `command-prompt -I "#S" { rename-session "%%" }`,
			wantOK:      true,
		},
		{
			name:        "escaped key is unescaped",
			line:        `bind-key    -T prefix \"      split-window`,
			table:       "prefix",
			wantKey:     `"`,
			wantCommand: "split-window",
			wantOK:      true,
		},
		{
			name:        "bound key with no command",
			line:        "bind-key    -T prefix F11",
			table:       "prefix",
			wantKey:     "F11",
			wantCommand: "",
			wantOK:      true,
		},
		{
			name:   "different table is not matched",
			line:   "bind-key    -T copy-mode n send-keys -X search-again",
			table:  "prefix",
			wantOK: false,
		},
		{
			name:   "blank line",
			line:   "",
			table:  "prefix",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, command, ok := splitBindingLine(tt.line, tt.table)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if key != tt.wantKey {
				t.Errorf("key = %q, want %q", key, tt.wantKey)
			}
			if command != tt.wantCommand {
				t.Errorf("command = %q, want %q", command, tt.wantCommand)
			}
		})
	}
}

package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestResolveAgentOwner_RecordsAgentPIDAndSessionName covers the gt-85p lock
// writer defect: the lock must identify the agent's own long-lived process and
// a real session id, not the short-lived CLI process that wrote it and not the
// TMUX_PANE pane id.
func TestResolveAgentOwner_RecordsAgentPIDAndSessionName(t *testing.T) {
	origExecCommand := execCommand
	defer func() { execCommand = origExecCommand }()
	t.Setenv("TMUX_PANE", "%20")

	var capturedArgs []string
	execCommand = func(name string, args ...string) interface{ Output() ([]byte, error) } {
		capturedArgs = args
		return &mockCmd{output: []byte("gt-rictus\t4242\n")}
	}

	owner := ResolveAgentOwner("gastown/rictus")

	if owner.PID != 4242 {
		t.Errorf("PID = %d, want 4242 (the tmux pane process, not the CLI process %d)", owner.PID, os.Getpid())
	}
	if owner.PID == os.Getpid() {
		t.Error("PID is the calling CLI process; the lock would go stale as soon as gt exits")
	}
	if owner.SessionID != "gt-rictus" {
		t.Errorf("SessionID = %q, want %q (session name, not a pane id)", owner.SessionID, "gt-rictus")
	}
	if owner.PaneID != "%20" {
		t.Errorf("PaneID = %q, want %q", owner.PaneID, "%20")
	}
	if owner.Source != OwnerSourceTmuxPane {
		t.Errorf("Source = %q, want %q", owner.Source, OwnerSourceTmuxPane)
	}

	foundPane := false
	for i, arg := range capturedArgs {
		if arg == "-t" && i+1 < len(capturedArgs) && capturedArgs[i+1] == "%20" {
			foundPane = true
		}
	}
	if !foundPane {
		t.Errorf("tmux args = %v, want the query targeted at pane %%20", capturedArgs)
	}
}

func TestResolveAgentOwner_FallsBackWhenTmuxUnusable(t *testing.T) {
	origExecCommand := execCommand
	defer func() { execCommand = origExecCommand }()

	tests := []struct {
		name    string
		pane    string
		output  string
		execErr error
	}{
		{name: "no tmux pane", pane: ""},
		{name: "tmux query fails", pane: "%20", execErr: fmt.Errorf("no server running")},
		{name: "unexpanded format string", pane: "%20", output: "#{session_name}\t#{pane_pid}\n"},
		{name: "unparseable pid", pane: "%20", output: "gt-rictus\tnot-a-pid\n"},
		{name: "missing field", pane: "%20", output: "gt-rictus\n"},
		{name: "zero pid", pane: "%20", output: "gt-rictus\t0\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TMUX_PANE", tt.pane)
			execCommand = func(name string, args ...string) interface{ Output() ([]byte, error) } {
				return &mockCmd{output: []byte(tt.output), err: tt.execErr}
			}

			owner := ResolveAgentOwner("gastown/rictus")

			if owner.PID != os.Getpid() {
				t.Errorf("PID = %d, want fallback to %d", owner.PID, os.Getpid())
			}
			if owner.SessionID != "gastown/rictus" {
				t.Errorf("SessionID = %q, want fallback %q", owner.SessionID, "gastown/rictus")
			}
			// The fallback must announce that its PID is not agent-anchored so
			// consumers do not read a dead PID as proof the agent exited.
			if owner.Source != OwnerSourceProcess {
				t.Errorf("Source = %q, want %q", owner.Source, OwnerSourceProcess)
			}
		})
	}
}

func TestLock_AcquireAs_WritesOwnerFacts(t *testing.T) {
	workerDir := filepath.Join(t.TempDir(), "worker")
	if err := os.MkdirAll(workerDir, 0755); err != nil {
		t.Fatal(err)
	}

	l := New(workerDir)
	owner := Owner{PID: os.Getpid(), SessionID: "gt-rictus", PaneID: "%20", Source: OwnerSourceTmuxPane}
	if err := l.AcquireAs(owner); err != nil {
		t.Fatalf("AcquireAs() error = %v", err)
	}

	info, err := l.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if info.PID != owner.PID {
		t.Errorf("PID = %d, want %d", info.PID, owner.PID)
	}
	if info.SessionID != "gt-rictus" {
		t.Errorf("SessionID = %q, want %q", info.SessionID, "gt-rictus")
	}
	if info.PaneID != "%20" {
		t.Errorf("PaneID = %q, want %q", info.PaneID, "%20")
	}
	if !info.PIDIsAgentAnchored() {
		t.Errorf("PIDIsAgentAnchored() = false, want true (pid_source = %q)", info.PIDSource)
	}
	if !info.OwnerAlive() {
		t.Error("OwnerAlive() = false for this live process")
	}
}

func TestLockInfo_PIDIsAgentAnchored(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{OwnerSourceTmuxPane, true},
		{OwnerSourceProcess, false},
		{"", false},
	}
	for _, tt := range tests {
		info := &LockInfo{PIDSource: tt.source}
		if got := info.PIDIsAgentAnchored(); got != tt.want {
			t.Errorf("PIDIsAgentAnchored() with source %q = %v, want %v", tt.source, got, tt.want)
		}
	}
}

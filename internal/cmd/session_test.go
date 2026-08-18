package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/tmux"
)

func TestSessionInfoJSONOutput(t *testing.T) {
	info := &polecat.SessionInfo{
		Polecat:   "alpha",
		SessionID: "gt-alpha",
		Running:   true,
		RigName:   "gastown",
		Attached:  false,
		Created:   time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC),
		Windows:   1,
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if parsed["polecat"] != "alpha" {
		t.Errorf("polecat = %v, want alpha", parsed["polecat"])
	}
	if parsed["session_id"] != "gt-alpha" {
		t.Errorf("session_id = %v, want gt-alpha", parsed["session_id"])
	}
	if parsed["running"] != true {
		t.Errorf("running = %v, want true", parsed["running"])
	}
	if parsed["rig_name"] != "gastown" {
		t.Errorf("rig_name = %v, want gastown", parsed["rig_name"])
	}
}

func TestSessionStatusCmdJSONFlagWiring(t *testing.T) {
	// Verify --json flag is registered on the session status command.
	// This catches regressions where flag binding is accidentally removed,
	// which would silently break formulas that depend on --json output.
	f := sessionStatusCmd.Flags().Lookup("json")
	if f == nil {
		t.Fatal("session status command missing --json flag")
	}
	if f.DefValue != "false" {
		t.Errorf("--json default = %q, want \"false\"", f.DefValue)
	}
}

func TestSessionHealthCmdFlagWiring(t *testing.T) {
	if sessionCmd.Commands() == nil {
		t.Fatal("session command has no subcommands")
	}

	f := sessionHealthCmd.Flags().Lookup("json")
	if f == nil {
		t.Fatal("session health command missing --json flag")
	}
	if f.DefValue != "false" {
		t.Errorf("--json default = %q, want \"false\"", f.DefValue)
	}

	f = sessionHealthCmd.Flags().Lookup("max-inactivity")
	if f == nil {
		t.Fatal("session health command missing --max-inactivity flag")
	}
	if f.DefValue != "0s" {
		t.Errorf("--max-inactivity default = %q, want \"0s\"", f.DefValue)
	}
}

func TestSessionHealthReportJSONContract(t *testing.T) {
	report := newSessionHealthReport("gt-vault", tmux.AgentDead, 30*time.Minute)
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if parsed["session"] != "gt-vault" {
		t.Errorf("session = %v, want gt-vault", parsed["session"])
	}
	if parsed["status"] != "agent-dead" {
		t.Errorf("status = %v, want agent-dead", parsed["status"])
	}
	if parsed["healthy"] != false {
		t.Errorf("healthy = %v, want false", parsed["healthy"])
	}
	if parsed["zombie"] != true {
		t.Errorf("zombie = %v, want true", parsed["zombie"])
	}
	if parsed["max_inactivity_seconds"] != float64(1800) {
		t.Errorf("max_inactivity_seconds = %v, want 1800", parsed["max_inactivity_seconds"])
	}
}

// TestSessionHealthReport_UnknownIsNotADeathReport pins the caller-facing half
// of gt-550. A report whose only negative signals are healthy=false and
// zombie=false looks exactly like a session-dead report, so the JSON must carry
// the distinction between "not alive" and "could not find out".
func TestSessionHealthReport_UnknownIsNotADeathReport(t *testing.T) {
	probeErr := errors.New("tmux server unreachable")
	report := newSessionHealthReportChecked("gt-vault", tmux.SessionUnknown, 0, probeErr)

	if report.Status == tmux.SessionDead.String() || report.Status == tmux.AgentDead.String() {
		t.Fatalf("status = %q; a failed probe must not be reported as a death state", report.Status)
	}
	if report.Known {
		t.Error("liveness_known = true for a failed probe")
	}
	if report.Healthy || report.Zombie {
		t.Errorf("healthy = %v zombie = %v, want both false", report.Healthy, report.Zombie)
	}
	if !strings.Contains(report.Detail, probeErr.Error()) {
		t.Errorf("detail = %q, want it to name the probe failure", report.Detail)
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if parsed["liveness_known"] != false {
		t.Errorf("liveness_known = %v, want false", parsed["liveness_known"])
	}
	if parsed["status"] != "liveness-unknown" {
		t.Errorf("status = %v, want liveness-unknown", parsed["status"])
	}
}

// TestSessionHealthDetail_SessionDeadNamesTheCause records why session-dead was
// read as a liveness finding for an agent address: the status is a fact about
// the NAME, and nothing in the output said so (gt-550).
func TestSessionHealthDetail_SessionDeadNamesTheCause(t *testing.T) {
	detail := sessionHealthDetail("gastown/furiosa", tmux.SessionDead, nil)
	if !strings.Contains(detail, "no tmux session named") {
		t.Errorf("detail = %q, want it to say no session answers to that name", detail)
	}
	if !strings.Contains(detail, "not a report about an agent process") {
		t.Errorf("detail = %q, want it to disclaim being an agent liveness finding", detail)
	}

	// Reports about states that ARE about the agent must keep saying so.
	if got := sessionHealthDetail("gt-vault", tmux.AgentDead, nil); !strings.Contains(got, "no agent process") {
		t.Errorf("agent-dead detail = %q, want it to describe the missing agent process", got)
	}
	if got := sessionHealthDetail("gt-vault", tmux.SessionHealthy, nil); got != "" {
		t.Errorf("healthy detail = %q, want empty", got)
	}
}

// TestRunSessionHealthSessionDeadStillExitsZero guards the contract this fix
// deliberately did not change: the help says the command exits successfully for
// all valid health states, and session-dead is one. Only a failed probe — an
// operational failure — returns non-zero.
func TestRunSessionHealthSessionDeadStillExitsZero(t *testing.T) {
	oldJSON := sessionHealthJSON
	oldMaxInactivity := sessionHealthMaxInactivity
	oldStdout := os.Stdout
	t.Cleanup(func() {
		sessionHealthJSON = oldJSON
		sessionHealthMaxInactivity = oldMaxInactivity
		os.Stdout = oldStdout
	})

	sessionHealthJSON = false
	sessionHealthMaxInactivity = 0
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	os.Stdout = w

	runErr := runSessionHealth(sessionHealthCmd, []string{"gt-session-health-test-nonexistent"})
	if closeErr := w.Close(); closeErr != nil {
		t.Fatalf("closing pipe writer: %v", closeErr)
	}
	os.Stdout = oldStdout
	if runErr != nil {
		t.Fatalf("runSessionHealth returned %v; a nonexistent session is a valid health state", runErr)
	}

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading stdout pipe: %v", err)
	}
	if !strings.Contains(string(out), "no tmux session named") {
		t.Errorf("output = %q, want it to explain that no session answers to that name", string(out))
	}
}

func TestRunSessionHealthJSONSessionDead(t *testing.T) {
	oldJSON := sessionHealthJSON
	oldMaxInactivity := sessionHealthMaxInactivity
	oldStdout := os.Stdout
	t.Cleanup(func() {
		sessionHealthJSON = oldJSON
		sessionHealthMaxInactivity = oldMaxInactivity
		os.Stdout = oldStdout
	})

	sessionHealthJSON = true
	sessionHealthMaxInactivity = 0
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	os.Stdout = w

	err = runSessionHealth(sessionHealthCmd, []string{"gt-session-health-test-nonexistent"})
	if closeErr := w.Close(); closeErr != nil {
		t.Fatalf("closing pipe writer: %v", closeErr)
	}
	os.Stdout = oldStdout
	if err != nil {
		t.Fatalf("runSessionHealth failed: %v", err)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading stdout pipe: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v\noutput: %s", err, string(data))
	}
	if parsed["session"] != "gt-session-health-test-nonexistent" {
		t.Errorf("session = %v, want gt-session-health-test-nonexistent", parsed["session"])
	}
	if parsed["status"] != "session-dead" {
		t.Errorf("status = %v, want session-dead", parsed["status"])
	}
	if parsed["healthy"] != false {
		t.Errorf("healthy = %v, want false", parsed["healthy"])
	}
	if parsed["zombie"] != false {
		t.Errorf("zombie = %v, want false", parsed["zombie"])
	}
}

func TestSessionInfoJSONOutputNotRunning(t *testing.T) {
	info := &polecat.SessionInfo{
		Polecat:   "beta",
		SessionID: "gt-beta",
		Running:   false,
		RigName:   "testrig",
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if parsed["running"] != false {
		t.Errorf("running = %v, want false", parsed["running"])
	}
}

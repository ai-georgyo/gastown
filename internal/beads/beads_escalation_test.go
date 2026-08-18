package beads

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatEscalationDescription(t *testing.T) {
	tests := []struct {
		name   string
		title  string
		fields *EscalationFields
		want   []string
		notIn  []string
	}{
		{
			name:   "nil fields returns title only",
			title:  "Test Escalation",
			fields: nil,
			want:   []string{"Test Escalation"},
			notIn:  []string{"severity:"},
		},
		{
			name:  "basic escalation",
			title: "Build failure",
			fields: &EscalationFields{
				Severity:    "high",
				Reason:      "Build failed 3 times",
				Source:      "patrol:deacon",
				EscalatedBy: "gastown/deacon",
				EscalatedAt: "2024-01-15T10:00:00Z",
			},
			want: []string{
				"Build failure",
				"severity: high",
				"reason: Build failed 3 times",
				"source: patrol:deacon",
				"escalated_by: gastown/deacon",
				"escalated_at: 2024-01-15T10:00:00Z",
			},
		},
		{
			name:  "acknowledged escalation",
			title: "Agent stuck",
			fields: &EscalationFields{
				Severity:    "medium",
				Reason:      "Agent not responding",
				EscalatedBy: "gastown/witness",
				EscalatedAt: "2024-01-15T10:00:00Z",
				AckedBy:     "gastown/crew/joe",
				AckedAt:     "2024-01-15T10:05:00Z",
			},
			want: []string{
				"severity: medium",
				"acked_by: gastown/crew/joe",
				"acked_at: 2024-01-15T10:05:00Z",
			},
		},
		{
			name:  "closed escalation",
			title: "Disk full",
			fields: &EscalationFields{
				Severity:     "critical",
				Reason:       "Disk >95%",
				EscalatedBy:  "gastown/deacon",
				EscalatedAt:  "2024-01-15T10:00:00Z",
				ClosedBy:     "human",
				ClosedReason: "Cleaned up temp files",
			},
			want: []string{
				"closed_by: human",
				"closed_reason: Cleaned up temp files",
			},
		},
		{
			name:  "null fields formatted explicitly",
			title: "New escalation",
			fields: &EscalationFields{
				Severity:    "low",
				Reason:      "Minor issue",
				EscalatedBy: "test",
				EscalatedAt: "2024-01-01T00:00:00Z",
			},
			want: []string{
				"acked_by: null",
				"acked_at: null",
				"closed_by: null",
				"closed_reason: null",
				"related_bead: null",
				"original_severity: null",
			},
		},
		{
			name:  "reescalation fields",
			title: "Bumped escalation",
			fields: &EscalationFields{
				Severity:          "high",
				Reason:            "Stale for 2h",
				EscalatedBy:       "patrol",
				EscalatedAt:       "2024-01-15T08:00:00Z",
				OriginalSeverity:  "low",
				ReescalationCount: 2,
				LastReescalatedAt: "2024-01-15T10:00:00Z",
				LastReescalatedBy: "deacon",
			},
			want: []string{
				"original_severity: low",
				"reescalation_count: 2",
				"last_reescalated_at: 2024-01-15T10:00:00Z",
				"last_reescalated_by: deacon",
			},
		},
		{
			name:  "fingerprint field",
			title: "Repeated alert",
			fields: &EscalationFields{
				Severity:    "medium",
				Reason:      "control-plane timeout",
				EscalatedBy: "deacon",
				EscalatedAt: "2024-01-15T10:00:00Z",
				Fingerprint: "escalation-fp:abc123def456",
			},
			want: []string{
				"fingerprint: escalation-fp:abc123def456",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatEscalationDescription(tt.title, tt.fields)
			for _, line := range tt.want {
				if !strings.Contains(got, line) {
					t.Errorf("missing line %q in output:\n%s", line, got)
				}
			}
			for _, line := range tt.notIn {
				if strings.Contains(got, line) {
					t.Errorf("unexpected %q in output:\n%s", line, got)
				}
			}
		})
	}
}

func TestParseEscalationFields(t *testing.T) {
	tests := []struct {
		name string
		desc string
		want *EscalationFields
	}{
		{
			name: "empty description",
			desc: "",
			want: &EscalationFields{},
		},
		{
			name: "full escalation",
			desc: `Escalation: Build failure

severity: high
reason: Build failed 3 times
source: patrol:deacon
escalated_by: gastown/deacon
escalated_at: 2024-01-15T10:00:00Z
acked_by: gastown/crew/joe
acked_at: 2024-01-15T10:05:00Z
closed_by: null
closed_reason: null
related_bead: gt-abc123
original_severity: medium
reescalation_count: 1
last_reescalated_at: 2024-01-15T09:30:00Z
last_reescalated_by: deacon
fingerprint: escalation-fp:abc123def456`,
			want: &EscalationFields{
				Severity:          "high",
				Reason:            "Build failed 3 times",
				Source:            "patrol:deacon",
				EscalatedBy:       "gastown/deacon",
				EscalatedAt:       "2024-01-15T10:00:00Z",
				AckedBy:           "gastown/crew/joe",
				AckedAt:           "2024-01-15T10:05:00Z",
				ClosedBy:          "",
				ClosedReason:      "",
				RelatedBead:       "gt-abc123",
				OriginalSeverity:  "medium",
				ReescalationCount: 1,
				LastReescalatedAt: "2024-01-15T09:30:00Z",
				LastReescalatedBy: "deacon",
				Fingerprint:       "escalation-fp:abc123def456",
			},
		},
		{
			name: "null values become empty strings",
			desc: "severity: critical\nsource: null\nacked_by: null",
			want: &EscalationFields{
				Severity: "critical",
				Source:   "",
				AckedBy:  "",
			},
		},
		{
			name: "invalid reescalation_count ignored",
			desc: "severity: low\nreescalation_count: not-a-number",
			want: &EscalationFields{
				Severity:          "low",
				ReescalationCount: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseEscalationFields(tt.desc)
			if got.Severity != tt.want.Severity {
				t.Errorf("Severity = %q, want %q", got.Severity, tt.want.Severity)
			}
			if got.Reason != tt.want.Reason {
				t.Errorf("Reason = %q, want %q", got.Reason, tt.want.Reason)
			}
			if got.Source != tt.want.Source {
				t.Errorf("Source = %q, want %q", got.Source, tt.want.Source)
			}
			if got.EscalatedBy != tt.want.EscalatedBy {
				t.Errorf("EscalatedBy = %q, want %q", got.EscalatedBy, tt.want.EscalatedBy)
			}
			if got.EscalatedAt != tt.want.EscalatedAt {
				t.Errorf("EscalatedAt = %q, want %q", got.EscalatedAt, tt.want.EscalatedAt)
			}
			if got.AckedBy != tt.want.AckedBy {
				t.Errorf("AckedBy = %q, want %q", got.AckedBy, tt.want.AckedBy)
			}
			if got.AckedAt != tt.want.AckedAt {
				t.Errorf("AckedAt = %q, want %q", got.AckedAt, tt.want.AckedAt)
			}
			if got.ClosedBy != tt.want.ClosedBy {
				t.Errorf("ClosedBy = %q, want %q", got.ClosedBy, tt.want.ClosedBy)
			}
			if got.ClosedReason != tt.want.ClosedReason {
				t.Errorf("ClosedReason = %q, want %q", got.ClosedReason, tt.want.ClosedReason)
			}
			if got.RelatedBead != tt.want.RelatedBead {
				t.Errorf("RelatedBead = %q, want %q", got.RelatedBead, tt.want.RelatedBead)
			}
			if got.OriginalSeverity != tt.want.OriginalSeverity {
				t.Errorf("OriginalSeverity = %q, want %q", got.OriginalSeverity, tt.want.OriginalSeverity)
			}
			if got.ReescalationCount != tt.want.ReescalationCount {
				t.Errorf("ReescalationCount = %d, want %d", got.ReescalationCount, tt.want.ReescalationCount)
			}
			if got.LastReescalatedAt != tt.want.LastReescalatedAt {
				t.Errorf("LastReescalatedAt = %q, want %q", got.LastReescalatedAt, tt.want.LastReescalatedAt)
			}
			if got.LastReescalatedBy != tt.want.LastReescalatedBy {
				t.Errorf("LastReescalatedBy = %q, want %q", got.LastReescalatedBy, tt.want.LastReescalatedBy)
			}
			if got.Fingerprint != tt.want.Fingerprint {
				t.Errorf("Fingerprint = %q, want %q", got.Fingerprint, tt.want.Fingerprint)
			}
		})
	}
}

func TestEscalationFieldsRoundTrip(t *testing.T) {
	original := &EscalationFields{
		Severity:          "high",
		Reason:            "Agent stuck for 1h",
		Source:            "patrol:witness",
		EscalatedBy:       "gastown/witness",
		EscalatedAt:       "2024-06-15T12:00:00Z",
		AckedBy:           "gastown/crew/joe",
		AckedAt:           "2024-06-15T12:05:00Z",
		RelatedBead:       "gt-stuck123",
		OriginalSeverity:  "medium",
		ReescalationCount: 1,
		LastReescalatedAt: "2024-06-15T11:30:00Z",
		LastReescalatedBy: "deacon",
		Fingerprint:       "escalation-fp:feedface1234",
	}

	formatted := FormatEscalationDescription("Escalation: Agent stuck", original)
	parsed := ParseEscalationFields(formatted)

	if parsed.Severity != original.Severity {
		t.Errorf("Severity: got %q, want %q", parsed.Severity, original.Severity)
	}
	if parsed.Reason != original.Reason {
		t.Errorf("Reason: got %q, want %q", parsed.Reason, original.Reason)
	}
	if parsed.Source != original.Source {
		t.Errorf("Source: got %q, want %q", parsed.Source, original.Source)
	}
	if parsed.EscalatedBy != original.EscalatedBy {
		t.Errorf("EscalatedBy: got %q, want %q", parsed.EscalatedBy, original.EscalatedBy)
	}
	if parsed.EscalatedAt != original.EscalatedAt {
		t.Errorf("EscalatedAt: got %q, want %q", parsed.EscalatedAt, original.EscalatedAt)
	}
	if parsed.AckedBy != original.AckedBy {
		t.Errorf("AckedBy: got %q, want %q", parsed.AckedBy, original.AckedBy)
	}
	if parsed.AckedAt != original.AckedAt {
		t.Errorf("AckedAt: got %q, want %q", parsed.AckedAt, original.AckedAt)
	}
	if parsed.RelatedBead != original.RelatedBead {
		t.Errorf("RelatedBead: got %q, want %q", parsed.RelatedBead, original.RelatedBead)
	}
	if parsed.OriginalSeverity != original.OriginalSeverity {
		t.Errorf("OriginalSeverity: got %q, want %q", parsed.OriginalSeverity, original.OriginalSeverity)
	}
	if parsed.ReescalationCount != original.ReescalationCount {
		t.Errorf("ReescalationCount: got %d, want %d", parsed.ReescalationCount, original.ReescalationCount)
	}
	if parsed.LastReescalatedAt != original.LastReescalatedAt {
		t.Errorf("LastReescalatedAt: got %q, want %q", parsed.LastReescalatedAt, original.LastReescalatedAt)
	}
	if parsed.LastReescalatedBy != original.LastReescalatedBy {
		t.Errorf("LastReescalatedBy: got %q, want %q", parsed.LastReescalatedBy, original.LastReescalatedBy)
	}
	if parsed.Fingerprint != original.Fingerprint {
		t.Errorf("Fingerprint: got %q, want %q", parsed.Fingerprint, original.Fingerprint)
	}
}

func TestFilterEscalationRecords(t *testing.T) {
	tests := []struct {
		name   string
		issues []*Issue
		want   []string
	}{
		{
			name: "mail copy collapses onto its record",
			issues: []*Issue{
				{ID: "hq-root", Labels: []string{"gt:escalation"}},
				{ID: "hq-mail", Labels: []string{"gt:escalation", "gt:message", "thread:hq-root"}},
			},
			want: []string{"hq-root"},
		},
		{
			name: "one row per recipient collapses to one row",
			issues: []*Issue{
				{ID: "hq-root", Labels: []string{"gt:escalation"}},
				{ID: "hq-mail-mayor", Labels: []string{"gt:escalation", "gt:message", "escalation:hq-root"}},
				{ID: "hq-mail-overseer", Labels: []string{"gt:escalation", "gt:message", "escalation:hq-root"}},
			},
			want: []string{"hq-root"},
		},
		{
			name: "mail copies survive when their record is absent",
			issues: []*Issue{
				{ID: "hq-mail-mayor", Labels: []string{"gt:escalation", "gt:message", "thread:hq-root"}},
				{ID: "hq-mail-overseer", Labels: []string{"gt:escalation", "gt:message", "thread:hq-root"}},
			},
			want: []string{"hq-mail-mayor"},
		},
		{
			name: "unrelated escalations are not collapsed together",
			issues: []*Issue{
				{ID: "hq-mail-a", Labels: []string{"gt:escalation", "gt:message", "thread:hq-root-a"}},
				{ID: "hq-mail-b", Labels: []string{"gt:escalation", "gt:message", "thread:hq-root-b"}},
			},
			want: []string{"hq-mail-a", "hq-mail-b"},
		},
		{
			name: "mail copy with no back-reference is kept",
			issues: []*Issue{
				{ID: "hq-mail-orphan", Labels: []string{"gt:escalation", "gt:message"}},
			},
			want: []string{"hq-mail-orphan"},
		},
		{
			name: "records only",
			issues: []*Issue{
				{ID: "hq-root-a", Labels: []string{"gt:escalation"}},
				{ID: "hq-root-b", Labels: []string{"gt:escalation"}},
			},
			want: []string{"hq-root-a", "hq-root-b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterEscalationRecords(tt.issues)
			// Assert on the row count against a known population: an
			// exit-status or "did not error" assertion passes even when the
			// filter has eaten every row (gt-c6x).
			if len(got) != len(tt.want) {
				t.Fatalf("filterEscalationRecords() returned %d rows, want %d: %#v", len(got), len(tt.want), got)
			}
			for i, want := range tt.want {
				if got[i].ID != want {
					t.Errorf("row %d = %s, want %s", i, got[i].ID, want)
				}
			}
		})
	}
}

// escalationListStub installs a fake bd that reproduces the two behaviours
// that together produced gt-c6x: escalation records are ephemeral wisps that
// `bd list` hides unless --include-infra is passed, and every escalation is
// also delivered as mail, so the mail copies carry gt:message.
func escalationListStub(t *testing.T) string {
	t.Helper()

	stubDir := t.TempDir()
	argsPath := filepath.Join(stubDir, "args.txt")

	const record = `{"id":"hq-wisp-1","title":"Disk full","description":"Disk full\n\nseverity: high\nescalated_by: deacon/boot\n","status":"open","priority":1,"type":"task","labels":["gt:escalation","severity:high"]}`
	const mailMayor = `{"id":"hq-mail-1","title":"[HIGH] Disk full","description":"Escalation ID: hq-wisp-1\nSeverity: high\n","status":"open","priority":1,"type":"task","labels":["gt:escalation","gt:message","msg-type:escalation","thread:hq-wisp-1"]}`
	const mailOverseer = `{"id":"hq-mail-2","title":"[HIGH] Disk full","description":"Escalation ID: hq-wisp-1\nSeverity: high\n","status":"open","priority":1,"type":"task","labels":["gt:escalation","gt:message","msg-type:escalation","thread:hq-wisp-1"]}`

	stubScript := `#!/bin/sh
sub=""
target=""
infra=0
for a in "$@"; do
  case "$a" in
    --include-infra) infra=1 ;;
    -*) ;;
    *)
      if [ -z "$sub" ]; then sub="$a"
      elif [ -z "$target" ]; then target="$a"
      fi
      ;;
  esac
done
if [ "$sub" = "list" ]; then
  for a in "$@"; do printf '%s
' "$a" >> "` + argsPath + `"; done
  if [ "$infra" = "1" ]; then
    printf '%s\n' '[` + record + `,` + mailMayor + `,` + mailOverseer + `]'
  else
    printf '%s\n' '[` + mailMayor + `,` + mailOverseer + `]'
  fi
  exit 0
fi
if [ "$sub" = "show" ]; then
  case "$target" in
    hq-wisp-1) printf '%s\n' '[` + record + `]' ;;
    hq-mail-1) printf '%s\n' '[` + mailMayor + `]' ;;
    hq-mail-2) printf '%s\n' '[` + mailOverseer + `]' ;;
    *) printf '%s\n' '[]' ;;
  esac
  exit 0
fi
printf '%s\n' '[]'
exit 0
`
	if err := os.WriteFile(filepath.Join(stubDir, "bd"), []byte(stubScript), 0755); err != nil {
		t.Fatalf("write bd stub: %v", err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ResetBdAllowStaleCacheForTest()

	return argsPath
}

// TestListEscalationsReturnsOpenEscalations is the regression test for gt-c6x:
// `gt escalate list` could never return a row. bd hides the wisp-backed
// escalation records, and the filter dropped every remaining mail copy, so the
// command reported a confident "No escalations found" with exit 0 while open
// escalations existed. Asserting the row count against a known population is
// the point — "the command succeeded" passes against the broken code.
func TestListEscalationsReturnsOpenEscalations(t *testing.T) {
	argsPath := escalationListStub(t)

	b := New(t.TempDir())
	got, err := b.ListEscalations()
	if err != nil {
		t.Fatalf("ListEscalations: %v", err)
	}

	// Population is one escalation: one record plus two mail copies.
	if len(got) != 1 {
		t.Fatalf("ListEscalations() returned %d rows, want 1: %#v", len(got), got)
	}
	if got[0].ID != "hq-wisp-1" {
		t.Errorf("ListEscalations() = %s, want the escalation record hq-wisp-1", got[0].ID)
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read stub args: %v", err)
	}
	if !strings.Contains(string(argsData), "--include-infra") {
		t.Errorf("bd list args missing --include-infra (escalation records are wisps):\n%s", argsData)
	}
}

// TestListEscalationsAgreesWithShow covers the second half of gt-c6x: `gt
// escalate show <id>` rendered a bead that `gt escalate list` claimed did not
// exist. Every id the list returns must resolve through the show path.
func TestListEscalationsAgreesWithShow(t *testing.T) {
	escalationListStub(t)

	b := New(t.TempDir())
	listed, err := b.ListEscalations()
	if err != nil {
		t.Fatalf("ListEscalations: %v", err)
	}
	if len(listed) == 0 {
		t.Fatal("ListEscalations() returned no rows, nothing to reconcile with show")
	}

	for _, issue := range listed {
		shown, fields, err := b.GetEscalationBead(issue.ID)
		if err != nil {
			t.Fatalf("GetEscalationBead(%s): %v", issue.ID, err)
		}
		if shown == nil {
			t.Fatalf("list returned %s but show reports it does not exist", issue.ID)
		}
		if shown.ID != issue.ID {
			t.Errorf("show returned %s for listed id %s", shown.ID, issue.ID)
		}
		if fields.Severity == "" {
			t.Errorf("show parsed no severity for %s", issue.ID)
		}
	}
}

func TestListAllEscalationsIncludesClosed(t *testing.T) {
	argsPath := escalationListStub(t)

	b := New(t.TempDir())
	got, err := b.ListAllEscalations()
	if err != nil {
		t.Fatalf("ListAllEscalations: %v", err)
	}
	if len(got) != 1 || got[0].ID != "hq-wisp-1" {
		t.Fatalf("ListAllEscalations() = %#v, want the escalation record hq-wisp-1", got)
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read stub args: %v", err)
	}
	args := string(argsData)
	for _, want := range []string{"--status=all", "--include-infra"} {
		if !strings.Contains(args, want) {
			t.Errorf("bd list args missing %s:\n%s", want, args)
		}
	}
}

func TestEscalationRecordID(t *testing.T) {
	tests := []struct {
		name  string
		issue *Issue
		want  string
	}{
		{"escalation label wins", &Issue{Labels: []string{"thread:hq-b", "escalation:hq-a"}}, "hq-a"},
		{"thread label is the fallback", &Issue{Labels: []string{"gt:message", "thread:hq-b"}}, "hq-b"},
		{"no back-reference", &Issue{Labels: []string{"gt:escalation", "gt:message"}}, ""},
		{"gt:escalation is not a back-reference", &Issue{Labels: []string{"gt:escalation"}}, ""},
		{"nil issue", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escalationRecordID(tt.issue); got != tt.want {
				t.Errorf("escalationRecordID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBumpSeverity(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"low", "medium"},
		{"medium", "high"},
		{"high", "critical"},
		{"critical", "critical"}, // already at max
		{"unknown", "critical"},  // default fallthrough
		{"", "critical"},         // empty defaults to critical
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := bumpSeverity(tt.input)
			if got != tt.want {
				t.Errorf("bumpSeverity(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestCreateEscalationBead_PassesDescriptionViaStdin verifies that
// CreateEscalationBead passes the multi-line description through bd's stdin
// (--body-file=-) rather than embedding newlines in --description=...
//
// Regression test for dc-1bxe: bd 1.0.3+ rejects newlines inside --description
// flag values, which broke `gt escalate` for any escalation containing the
// structured YAML metadata block (severity, reason, escalated_by, etc.).
func TestCreateEscalationBead_PassesDescriptionViaStdin(t *testing.T) {
	stubDir := t.TempDir()
	argsPath := filepath.Join(stubDir, "args.txt")
	stdinPath := filepath.Join(stubDir, "stdin.txt")

	// Stub bd: write each arg on its own line to args.txt, capture stdin to
	// stdin.txt, and emit a minimal valid issue JSON so unmarshal succeeds.
	stubScript := `#!/bin/sh
for a in "$@"; do
  printf '%s\n' "$a" >> "` + argsPath + `"
done
cat > "` + stdinPath + `"
echo '{"id":"dc-test1","title":"x","status":"open","priority":2,"type":"task","labels":["gt:escalation"]}'
exit 0
`
	stubPath := filepath.Join(stubDir, "bd")
	if err := os.WriteFile(stubPath, []byte(stubScript), 0755); err != nil {
		t.Fatalf("write bd stub: %v", err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Reset --allow-stale capability cache so the stub gets probed fresh.
	ResetBdAllowStaleCacheForTest()

	b := New(t.TempDir())
	fields := &EscalationFields{
		Severity:    "high",
		Reason:      "multi-line\nreason\nwith embedded newlines",
		EscalatedBy: "test/agent",
		EscalatedAt: "2026-05-08T15:00:00Z",
		Fingerprint: "escalation-fp:abc123def456",
	}

	if _, err := b.CreateEscalationBead("Test escalation", fields); err != nil {
		t.Fatalf("CreateEscalationBead: %v", err)
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read stub args: %v", err)
	}
	args := string(argsData)

	// Must use --body-file=- to read description from stdin.
	if !strings.Contains(args, "--body-file=-") {
		t.Errorf("expected --body-file=- in bd args, got:\n%s", args)
	}
	if !strings.Contains(args, "--labels=escalation-fp:abc123def456") {
		t.Errorf("expected fingerprint label in bd args, got:\n%s", args)
	}
	// Must NOT pass --description=... at all (any --description value would
	// embed the newline-containing structured description and fail bd 1.0.3+).
	for _, line := range strings.Split(args, "\n") {
		if strings.HasPrefix(line, "--description=") {
			t.Errorf("--description=... must not be used (bd rejects newlines), got %q", line)
		}
	}

	stdinData, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("read stub stdin: %v", err)
	}
	stdin := string(stdinData)
	// The structured description must reach bd via stdin.
	wantInStdin := []string{
		"Test escalation",
		"severity: high",
		"escalated_by: test/agent",
	}
	for _, want := range wantInStdin {
		if !strings.Contains(stdin, want) {
			t.Errorf("expected stdin to contain %q, got:\n%s", want, stdin)
		}
	}
	// Sanity: stdin must contain newlines (it's the multi-line description).
	if !strings.Contains(stdin, "\n") {
		t.Errorf("expected stdin to be multi-line, got %q", stdin)
	}
}

package beads

import (
	"reflect"
	"testing"
)

func TestCanonicalAssignee(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"deacon keeps bare form", "deacon", "deacon"},
		{"deacon loses trailing slash", "deacon/", "deacon"},
		{"mayor loses trailing slash", "mayor/", "mayor"},
		{"surrounding space trimmed", "  deacon/  ", "deacon"},
		{"repeated slashes trimmed", "deacon//", "deacon"},
		{"rig agent unchanged", "gastown/witness", "gastown/witness"},
		{"polecat unchanged", "gastown/polecats/nux", "gastown/polecats/nux"},
		{"dog unchanged", "deacon/dogs/rex", "deacon/dogs/rex"},
		{"boot unchanged", "deacon/boot", "deacon/boot"},
		{"mail queue pseudo-assignee unchanged", "queue:build", "queue:build"},
		{"channel pseudo-assignee unchanged", "channel:ops", "channel:ops"},
		{"empty unassign value preserved", "", ""},
		{"slash-only input preserved rather than emptied", "/", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanonicalAssignee(tt.in); got != tt.want {
				t.Errorf("CanonicalAssignee(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSameAssignee(t *testing.T) {
	if !SameAssignee("deacon", "deacon/") {
		t.Error(`SameAssignee("deacon", "deacon/") = false, want true`)
	}
	if SameAssignee("deacon", "mayor") {
		t.Error(`SameAssignee("deacon", "mayor") = true, want false`)
	}
}

func TestAssigneeQueryVariants(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"deacon", []string{"deacon", "deacon/"}},
		{"deacon/", []string{"deacon", "deacon/"}},
		{"mayor/", []string{"mayor", "mayor/"}},
		{"gastown/witness", []string{"gastown/witness"}},
		{"gastown/polecats/nux", []string{"gastown/polecats/nux"}},
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := AssigneeQueryVariants(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("AssigneeQueryVariants(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestNormalizeAssigneeArgs_Writes covers the boundary that makes the slashed
// form unrepresentable in new records: no matter how a caller spells the
// identity, what reaches bd's assignee column is canonical.
func TestNormalizeAssigneeArgs_Writes(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "update with inline value",
			in:   []string{"update", "hq-wisp-1", "--status=hooked", "--assignee=deacon/"},
			want: []string{"update", "hq-wisp-1", "--status=hooked", "--assignee=deacon"},
		},
		{
			name: "create with separate value",
			in:   []string{"create", "--assignee", "deacon/", "--", "subject"},
			want: []string{"create", "--assignee", "deacon", "--", "subject"},
		},
		{
			name: "already canonical is untouched",
			in:   []string{"update", "hq-1", "--assignee=deacon"},
			want: []string{"update", "hq-1", "--assignee=deacon"},
		},
		{
			name: "rig-level identity is untouched",
			in:   []string{"update", "gt-1", "--assignee=gastown/witness"},
			want: []string{"update", "gt-1", "--assignee=gastown/witness"},
		},
		{
			name: "empty unassign value survives",
			in:   []string{"update", "gt-1", "--status=open", "--assignee="},
			want: []string{"update", "gt-1", "--status=open", "--assignee="},
		},
		{
			name: "trailing --assignee with no value is left alone",
			in:   []string{"update", "gt-1", "--assignee"},
			want: []string{"update", "gt-1", "--assignee"},
		},
		{
			name: "other flags are preserved around the rewrite",
			in:   []string{"update", "hq-1", "--assignee", "mayor/", "--status=hooked", "--json"},
			want: []string{"update", "hq-1", "--assignee", "mayor", "--status=hooked", "--json"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeAssigneeArgs(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NormalizeAssigneeArgs(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestNormalizeAssigneeArgs_ReadsUntouched guards the migration path: readers
// must still be able to ask for the legacy trailing-slash form explicitly, or
// rows written before this change become invisible.
func TestNormalizeAssigneeArgs_ReadsUntouched(t *testing.T) {
	for _, args := range [][]string{
		{"list", "--assignee=deacon/", "--json"},
		{"query", "--json", `assignee="deacon/"`},
		{"show", "hq-1", "--json"},
	} {
		got := NormalizeAssigneeArgs(args)
		if !reflect.DeepEqual(got, args) {
			t.Errorf("NormalizeAssigneeArgs(%v) rewrote a read command to %v", args, got)
		}
	}
}

package doctor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/version"
)

func TestSupersededResult(t *testing.T) {
	const (
		oldStore = "/nix/store/9jj3g3iq-gt-1.0.0/bin/gt"
		newStore = "/nix/store/57g9y8sy-gt-1.0.0/bin/gt"
	)

	tests := []struct {
		name       string
		info       *version.BinarySkewInfo
		procs      []version.SupersededProcess
		wantStatus CheckStatus
		wantDetail string
	}{
		{
			name: "running the installed binary",
			info: &version.BinarySkewInfo{
				RunningPath:   newStore,
				InstalledPath: newStore,
			},
			wantStatus: StatusOK,
		},
		{
			// Skew we cannot measure must not read as a failure: this check
			// runs on machines with no canonical install at all.
			name: "undetermined skew is not a warning",
			info: &version.BinarySkewInfo{
				Skipped:    true,
				SkipReason: "no gt found at a canonical install location",
			},
			wantStatus: StatusOK,
			wantDetail: "no gt found",
		},
		{
			name: "superseded binary warns and counts live processes",
			info: &version.BinarySkewInfo{
				RunningPath:   oldStore,
				PathPath:      oldStore,
				InstalledPath: newStore,
				InstalledFrom: "/home/u/.nix-profile/bin/gt",
				Superseded:    true,
				PathShadowed:  true,
			},
			procs: []version.SupersededProcess{
				{PID: 1198051, Exe: oldStore},
				{PID: 1198052, Exe: oldStore},
			},
			wantStatus: StatusWarning,
			wantDetail: "2 live gt process(es)",
		},
		{
			// PATH can shadow the install even when this process happens to
			// be the installed binary — the next `gt` a script runs will not be.
			name: "path shadowing alone still warns",
			info: &version.BinarySkewInfo{
				RunningPath:   newStore,
				PathPath:      oldStore,
				InstalledPath: newStore,
				InstalledFrom: "/home/u/.nix-profile/bin/gt",
				PathShadowed:  true,
			},
			wantStatus: StatusWarning,
			wantDetail: "which gt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := supersededResult("superseded-binary", tt.info, tt.procs)

			if got.Status != tt.wantStatus {
				t.Errorf("Status = %v, want %v (message %q)", got.Status, tt.wantStatus, got.Message)
			}
			if tt.wantDetail != "" && !strings.Contains(strings.Join(got.Details, "\n"), tt.wantDetail) {
				t.Errorf("Details = %v, missing %q", got.Details, tt.wantDetail)
			}
			if tt.wantStatus == StatusWarning && got.FixHint == "" {
				t.Error("warning result has no FixHint")
			}
		})
	}
}

func TestSupersededResultTruncatesProcessList(t *testing.T) {
	info := &version.BinarySkewInfo{
		RunningPath:   "/nix/store/old/bin/gt",
		InstalledPath: "/nix/store/new/bin/gt",
		InstalledFrom: "/home/u/.nix-profile/bin/gt",
		Superseded:    true,
	}
	procs := make([]version.SupersededProcess, maxListedSupersededPIDs+5)
	for i := range procs {
		procs[i] = version.SupersededProcess{PID: 1000 + i, Exe: info.RunningPath}
	}

	details := strings.Join(supersededResult("superseded-binary", info, procs).Details, "\n")
	if !strings.Contains(details, "and 5 more") {
		t.Errorf("expected the pid list to be truncated, got:\n%s", details)
	}
	// One line per executable, not one per process.
	if !strings.Contains(details, fmt.Sprintf("%d × %s", len(procs), info.RunningPath)) {
		t.Errorf("expected processes grouped by executable, got:\n%s", details)
	}
}

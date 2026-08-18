package doctor

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/steveyegge/gastown/internal/version"
)

// maxListedSupersededPIDs caps how many PIDs are listed per executable. A
// town-wide skew is dozens of sessions running the same binary; the count and
// the executable are the finding, and a handful of PIDs is enough to act on.
const maxListedSupersededPIDs = 8

// SupersededBinaryCheck verifies this process is running the installed gt, not
// a superseded store path a long-lived session pinned in $PATH. This is
// distinct from StaleBinaryCheck: that one asks whether the *installed* binary
// is behind the repo, this one asks whether the *running* binary is the
// installed one at all. Both can be green while gt still misbehaves, which is
// what made this skew so hard to see. (gt-3pk)
type SupersededBinaryCheck struct {
	BaseCheck
}

// NewSupersededBinaryCheck creates a new superseded binary check.
func NewSupersededBinaryCheck() *SupersededBinaryCheck {
	return &SupersededBinaryCheck{
		BaseCheck: BaseCheck{
			CheckName:        "superseded-binary",
			CheckDescription: "Check this session is running the installed gt binary",
			CheckCategory:    CategoryInfrastructure,
		},
	}
}

// Run checks whether the running binary is the installed one.
func (c *SupersededBinaryCheck) Run(ctx *CheckContext) *CheckResult {
	info := version.CheckBinarySkew()

	var procs []version.SupersededProcess
	if info.Superseded || info.PathShadowed {
		procs, _ = version.ScanSupersededProcesses(info.InstalledPaths)
	}

	return supersededResult(c.Name(), info, procs)
}

// supersededResult maps a completed skew check to a doctor CheckResult. It is
// pure (no filesystem or process access) so it can be unit-tested directly.
func supersededResult(name string, info *version.BinarySkewInfo, procs []version.SupersededProcess) *CheckResult {
	if info.Skipped {
		return &CheckResult{
			Name:    name,
			Status:  StatusOK,
			Message: "Binary skew check skipped",
			Details: []string{info.SkipReason},
		}
	}

	if !info.Superseded && !info.PathShadowed {
		return &CheckResult{
			Name:    name,
			Status:  StatusOK,
			Message: fmt.Sprintf("Running the installed gt (%s)", info.RunningPath),
		}
	}

	details := append(info.Details(), describeSupersededProcesses(procs)...)

	return &CheckResult{
		Name:    name,
		Status:  StatusWarning,
		Message: info.Describe(),
		Details: details,
		FixHint: "Restart the session (or invoke " + info.InstalledFrom + " explicitly) — rebuilding will not help",
	}
}

// describeSupersededProcesses renders the live-process scan as detail lines,
// grouped by executable so a 20-session skew reads as one or two findings
// rather than twenty.
func describeSupersededProcesses(procs []version.SupersededProcess) []string {
	if len(procs) == 0 {
		return nil
	}

	details := []string{fmt.Sprintf("%d live gt process(es) are not running the installed binary:", len(procs))}
	for _, group := range version.GroupByExe(procs) {
		details = append(details, fmt.Sprintf("  %d × %s", len(group.PIDs), group.Exe))
		details = append(details, "    pids: "+formatPIDs(group.PIDs))
	}
	return details
}

func formatPIDs(pids []int) string {
	shown := pids
	suffix := ""
	if len(shown) > maxListedSupersededPIDs {
		shown = shown[:maxListedSupersededPIDs]
		suffix = fmt.Sprintf(" … and %d more", len(pids)-maxListedSupersededPIDs)
	}

	parts := make([]string, len(shown))
	for i, pid := range shown {
		parts[i] = strconv.Itoa(pid)
	}
	return strings.Join(parts, " ") + suffix
}

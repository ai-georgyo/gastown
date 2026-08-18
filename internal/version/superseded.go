package version

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// homeInstallLocations are the canonical install locations under $HOME, most
// authoritative first:
//
//	~/.nix-profile/bin/gt   nix profile install (symlink that follows upgrades)
//	~/.local/bin/gt         Makefile INSTALL_DIR (`make install` / `make safe-install`)
var homeInstallLocations = []string{
	".nix-profile/bin/gt",
	".local/bin/gt",
}

// systemInstallLocations are the package-manager install locations. They rank
// below the home ones — this codebase's own installers write to those — but
// they still count as installed, so a Homebrew gt is never mistaken for a
// superseded one just because a stray ~/.local/bin/gt exists.
var systemInstallLocations = []string{
	"/opt/homebrew/bin/gt",
	"/usr/local/bin/gt",
	"/usr/bin/gt",
}

// BinarySkewInfo describes disagreement between three different answers to
// "which gt is this?":
//
//	RunningPath    the binary this process is actually executing
//	PathPath       the binary a fresh `gt` would execute through $PATH
//	InstalledPath  the binary at a canonical install location
//
// They diverge when a long-lived session pins a resolved store path in $PATH.
// An upgrade swaps the install symlink, but the session keeps executing — and
// keeps resolving `gt` to — the superseded store path it started with. Nothing
// re-execs the session, and `which gt` reports the pinned path, so the skew is
// invisible from inside the session unless something checks for it. (gt-3pk)
type BinarySkewInfo struct {
	RunningPath    string   // Resolved path of the running executable
	PathPath       string   // Resolved path `gt` resolves to through $PATH
	InstalledPath  string   // Resolved path of the installed gt
	InstalledFrom  string   // Unresolved install location, e.g. ~/.nix-profile/bin/gt
	InstalledPaths []string // Every resolved canonical install path
	RunningOnPath  bool     // True if the running executable's directory is on $PATH
	Superseded     bool     // True if the running binary is not the installed one
	PathShadowed   bool     // True if $PATH resolves gt to something other than the installed one
	Skipped        bool     // True if skew could not be determined
	SkipReason     string   // Human-readable reason the check was skipped
}

// Describe returns a one-line summary of the skew, suitable as a warning
// headline. It is only meaningful when i.Superseded or i.PathShadowed.
func (i *BinarySkewInfo) Describe() string {
	if i.Superseded {
		return fmt.Sprintf("Running a superseded gt binary (%s); installed gt is %s",
			i.RunningPath, i.InstalledPath)
	}
	if i.PathShadowed {
		return fmt.Sprintf("PATH resolves gt to %s, not the installed %s",
			i.PathPath, i.InstalledPath)
	}
	return "Running the installed gt binary"
}

// Details returns supporting lines for the skew, ordered most actionable first.
func (i *BinarySkewInfo) Details() []string {
	var details []string
	switch {
	case i.Superseded && i.RunningOnPath:
		// The pinned-session case: $PATH handed this process the superseded
		// binary, so a restart is the fix.
		details = append(details,
			"This session started before the current gt was installed and nothing re-execs it.",
			"Restart the session to pick up the installed binary.")
	case i.Superseded:
		// Reached deliberately (running a build output) or by a long-lived
		// process that resolved the install symlink before an upgrade.
		details = append(details,
			"The running binary is not at any canonical install location.",
			"Restart the process, or run "+i.InstalledFrom+", to use the installed binary.")
	}
	if i.PathShadowed {
		details = append(details,
			fmt.Sprintf("'which gt' reports %s and is not evidence about what is deployed.", i.PathPath),
			fmt.Sprintf("Invoke %s explicitly, or restart the session, to run the installed binary.", i.InstalledFrom))
	}
	return details
}

// CheckBinarySkew compares the running binary, the $PATH resolution of gt, and
// the installed gt. It shells out to nothing and is safe to call on every
// command; errors are folded into Skipped rather than returned.
func CheckBinarySkew() *BinarySkewInfo {
	running := ""
	if exe, err := os.Executable(); err == nil {
		running = resolveSymlinks(exe)
	}

	pathPath := ""
	if p, err := exec.LookPath("gt"); err == nil {
		pathPath = resolveSymlinks(p)
	}

	return classifyBinarySkew(running, pathPath, installedCandidates(), pathDirs())
}

// classifyBinarySkew is the pure core of CheckBinarySkew: it takes already
// resolved paths so it can be unit-tested without touching the filesystem.
// candidates maps unresolved install location -> resolved path, in preference
// order; pathDirs holds the resolved directories on $PATH.
func classifyBinarySkew(running, pathPath string, candidates []InstallCandidate, pathDirs []string) *BinarySkewInfo {
	info := &BinarySkewInfo{
		RunningPath: running,
		PathPath:    pathPath,
	}

	if running == "" {
		info.Skipped = true
		info.SkipReason = "cannot determine the running executable"
		return info
	}
	if len(candidates) == 0 {
		info.Skipped = true
		info.SkipReason = "no gt found at a canonical install location"
		return info
	}

	info.InstalledFrom = candidates[0].Location
	info.InstalledPath = candidates[0].Resolved
	for _, c := range candidates {
		info.InstalledPaths = append(info.InstalledPaths, c.Resolved)
	}
	info.RunningOnPath = containsPath(pathDirs, filepath.Dir(running))

	// Guard on the basename: a test binary or `go run` output living outside
	// every install location is not a gt install gone wrong.
	info.Superseded = filepath.Base(running) == "gt" && !matchesCandidate(candidates, running)
	info.PathShadowed = pathPath != "" && !matchesCandidate(candidates, pathPath)

	return info
}

// InstallCandidate is a canonical install location and what it resolves to.
type InstallCandidate struct {
	Location string // Unresolved, e.g. /home/u/.nix-profile/bin/gt
	Resolved string // Symlinks followed, e.g. /nix/store/<hash>-gt-1.0.0/bin/gt
}

// installedCandidates returns the canonical install locations that exist, most
// authoritative first. A binary matching *any* of them counts as installed:
// several install mechanisms are in use and none of them is wrong. Only a
// binary matching none of them — a superseded store path — is skew.
func installedCandidates() []InstallCandidate {
	home := os.Getenv("HOME")
	if home == "" {
		return nil
	}
	locations := make([]string, 0, len(homeInstallLocations)+len(systemInstallLocations))
	for _, rel := range homeInstallLocations {
		locations = append(locations, filepath.Join(home, rel))
	}
	locations = append(locations, systemInstallLocations...)

	candidates := make([]InstallCandidate, 0, len(locations))
	for _, location := range locations {
		if _, err := os.Stat(location); err != nil {
			continue
		}
		candidates = append(candidates, InstallCandidate{
			Location: location,
			Resolved: resolveSymlinks(location),
		})
	}
	return candidates
}

func matchesCandidate(candidates []InstallCandidate, path string) bool {
	for _, c := range candidates {
		if c.Resolved == path || c.Location == path {
			return true
		}
	}
	return false
}

// pathDirs returns the resolved directories on $PATH.
func pathDirs() []string {
	entries := filepath.SplitList(os.Getenv("PATH"))
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == "" {
			continue
		}
		dirs = append(dirs, resolveSymlinks(entry))
	}
	return dirs
}

func containsPath(dirs []string, dir string) bool {
	for _, d := range dirs {
		if d == dir {
			return true
		}
	}
	return false
}

// resolveSymlinks follows symlinks, falling back to the input when it cannot
// (a missing path is still worth reporting verbatim).
func resolveSymlinks(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// SupersededProcess is a live gt process executing something other than the
// installed binary.
type SupersededProcess struct {
	PID int
	Exe string
}

// ErrProcessScanUnavailable is returned when live processes cannot be
// enumerated (no /proc, e.g. on macOS).
var ErrProcessScanUnavailable = errors.New("process scan unavailable (no /proc)")

// ScanSupersededProcesses lists live gt processes whose executable is none of
// installedPaths. Processes owned by other users are skipped silently: their
// /proc/<pid>/exe is unreadable, which is not an error worth surfacing.
//
// This is the town-wide view of the skew CheckBinarySkew reports for one
// process — it is what identifies which sessions still need restarting.
// Results are sorted by executable then PID so repeated runs are comparable.
func ScanSupersededProcesses(installedPaths []string) ([]SupersededProcess, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, ErrProcessScanUnavailable
	}

	var stale []SupersededProcess
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		exe, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
		if err != nil {
			continue
		}
		// A binary replaced on disk while running reads as "<path> (deleted)".
		exe = strings.TrimSuffix(exe, " (deleted)")
		if filepath.Base(exe) != "gt" || slices.Contains(installedPaths, exe) {
			continue
		}
		stale = append(stale, SupersededProcess{PID: pid, Exe: exe})
	}

	slices.SortFunc(stale, func(a, b SupersededProcess) int {
		if a.Exe != b.Exe {
			return strings.Compare(a.Exe, b.Exe)
		}
		return a.PID - b.PID
	})
	return stale, nil
}

// GroupByExe collapses a scan result into one entry per executable, preserving
// the scan order. A town-wide skew is dozens of processes running the same two
// or three binaries; the executables are the finding, the PIDs are the detail.
func GroupByExe(procs []SupersededProcess) []SupersededExeGroup {
	var groups []SupersededExeGroup
	for _, p := range procs {
		if n := len(groups); n > 0 && groups[n-1].Exe == p.Exe {
			groups[n-1].PIDs = append(groups[n-1].PIDs, p.PID)
			continue
		}
		groups = append(groups, SupersededExeGroup{Exe: p.Exe, PIDs: []int{p.PID}})
	}
	return groups
}

// SupersededExeGroup is the live processes sharing one superseded executable.
type SupersededExeGroup struct {
	Exe  string
	PIDs []int
}

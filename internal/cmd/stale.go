package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/version"
)

var staleJSON bool
var staleQuiet bool

var staleCmd = &cobra.Command{
	Use:     "stale",
	GroupID: GroupDiag,
	Short:   "Check if the gt binary is stale",
	Long: `Check if the gt binary was built from an older commit than a build ref.

This command compares the commit hash embedded in the binary at build time
with the resolved build branch of the gastown repository (main/master/carry/*).

Examples:
  gt stale              # Human-readable output
  gt stale --json       # Machine-readable JSON output
  gt stale --quiet      # Exit code only (0=stale, 1=fresh, 2=undetermined)

Exit codes:
  0 - Binary is stale
  1 - Binary is fresh (up to date)
  2 - Error or skipped (could not determine staleness)`,
	RunE: runStale,
}

func init() {
	staleCmd.Flags().BoolVar(&staleJSON, "json", false, "Output as JSON")
	staleCmd.Flags().BoolVarP(&staleQuiet, "quiet", "q", false, "Exit code only (0=stale, 1=fresh, 2=undetermined)")
	rootCmd.AddCommand(staleCmd)
}

// StaleOutput represents the JSON output structure.
type StaleOutput struct {
	Stale         bool   `json:"stale"`
	Forward       bool   `json:"forward"`
	OnMainBranch  bool   `json:"on_main_branch"`
	SafeToRebuild bool   `json:"safe_to_rebuild"`
	BinaryCommit  string `json:"binary_commit"`
	RepoCommit    string `json:"repo_commit"`
	CompareRef    string `json:"compare_ref,omitempty"`
	CommitsBehind int    `json:"commits_behind,omitempty"`
	Skipped       bool   `json:"skipped,omitempty"`
	SkipReason    string `json:"skip_reason,omitempty"`
	Error         string `json:"error,omitempty"`

	// Binary skew: which gt is actually running, versus which one is
	// installed. Independent of commit staleness — a fresh binary is no help
	// if this session is still executing the one it started with. (gt-3pk)
	Superseded   bool   `json:"superseded"`
	PathShadowed bool   `json:"path_shadowed"`
	RunningExe   string `json:"running_exe,omitempty"`
	PathExe      string `json:"path_exe,omitempty"`
	InstalledExe string `json:"installed_exe,omitempty"`
}

// applySkew copies the binary skew fields onto a staleness result. Skew is
// reported on every code path, including the ones where staleness itself could
// not be determined, because it is the more actionable of the two.
func applySkew(output *StaleOutput, skew *version.BinarySkewInfo) {
	output.RunningExe = skew.RunningPath
	output.PathExe = skew.PathPath
	output.InstalledExe = skew.InstalledPath
	output.Superseded = skew.Superseded
	output.PathShadowed = skew.PathShadowed
}

func runStale(cmd *cobra.Command, args []string) error {
	skew := version.CheckBinarySkew()

	// Find the gastown repo
	repoRoot, err := version.GetRepoRoot()
	if err != nil {
		if staleQuiet {
			return NewSilentExit(2)
		}
		if staleJSON {
			out := StaleOutput{Error: err.Error()}
			applySkew(&out, skew)
			return outputStaleJSON(out)
		}
		return fmt.Errorf("cannot find gastown repo: %w", err)
	}

	// Check staleness
	info := version.CheckStaleBinary(repoRoot)

	// Handle errors
	if info.Error != nil {
		if staleQuiet {
			return NewSilentExit(2)
		}
		if staleJSON {
			out := StaleOutput{Error: info.Error.Error()}
			applySkew(&out, skew)
			return outputStaleJSON(out)
		}
		return fmt.Errorf("staleness check failed: %w", info.Error)
	}

	// Quiet mode: just exit with appropriate code
	if staleQuiet {
		return NewSilentExit(staleQuietExitCode(info))
	}

	// Build output
	// SafeToRebuild requires: stale + forward-only + on a build branch.
	safeToRebuild := info.IsStale && info.IsForward && info.OnMainBranch
	output := StaleOutput{
		Stale:         info.IsStale,
		Forward:       info.IsForward,
		OnMainBranch:  info.OnMainBranch,
		SafeToRebuild: safeToRebuild,
		BinaryCommit:  info.BinaryCommit,
		RepoCommit:    info.RepoCommit,
		CompareRef:    info.CompareRef,
		CommitsBehind: info.CommitsBehind,
		Skipped:       info.Skipped,
		SkipReason:    info.SkipReason,
	}
	applySkew(&output, skew)

	if staleJSON {
		return outputStaleJSON(output)
	}

	if err := outputStaleText(output); err != nil {
		return err
	}
	return outputSkewText(skew)
}

func staleQuietExitCode(info *version.StaleBinaryInfo) int {
	if info.Skipped {
		return 2
	}
	if info.IsStale {
		return 0
	}
	return 1
}

func outputStaleJSON(output StaleOutput) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func outputStaleText(output StaleOutput) error {
	if output.Skipped {
		fmt.Printf("%s Binary staleness check skipped\n", style.Dim.Render("•"))
		fmt.Printf("  %s\n", output.SkipReason)
		fmt.Printf("  Binary: %s\n", version.ShortCommit(output.BinaryCommit))
		return nil
	}
	if output.Stale {
		fmt.Printf("%s Binary is stale\n", style.Warning.Render("⚠"))
		fmt.Printf("  Binary:   %s\n", version.ShortCommit(output.BinaryCommit))
		fmt.Printf("  Build ref (%s): %s\n", output.CompareRef, version.ShortCommit(output.RepoCommit))
		if output.CommitsBehind > 0 {
			fmt.Printf("  %s\n", style.Dim.Render(fmt.Sprintf("(%d commits behind %s)", output.CommitsBehind, output.CompareRef)))
		}
		if !output.Forward {
			fmt.Printf("  %s %s is NOT a descendant of binary commit (diverged or older)\n", style.Error.Render("✗"), output.CompareRef)
		}
		if !output.OnMainBranch {
			fmt.Printf("  %s source worktree is not on a build branch (compared against %s)\n", style.Warning.Render("⚠"), output.CompareRef)
		}
		if output.SafeToRebuild {
			fmt.Printf("\n  Safe to rebuild: run 'make build && make install'\n")
		} else {
			fmt.Printf("\n  %s NOT safe for automated rebuild (forward=%v, build_branch=%v)\n",
				style.Error.Render("✗"), output.Forward, output.OnMainBranch)
		}
	} else {
		fmt.Printf("%s Binary is fresh\n", style.Success.Render("✓"))
		fmt.Printf("  Commit: %s\n", version.ShortCommit(output.BinaryCommit))
		if output.CompareRef != "" {
			fmt.Printf("  %s\n", style.Dim.Render(fmt.Sprintf("(compared against %s)", output.CompareRef)))
		}
	}
	return nil
}

// outputSkewText prints the binary skew section, and only when there is skew:
// on a healthy machine the running binary is the installed one and there is
// nothing to say. Skew is printed after the staleness verdict because it
// overrides it — "binary is fresh" is about the installed gt, not the one you
// are talking to.
func outputSkewText(skew *version.BinarySkewInfo) error {
	if skew.Skipped || (!skew.Superseded && !skew.PathShadowed) {
		return nil
	}

	fmt.Printf("\n%s %s\n", style.Warning.Render("⚠"), skew.Describe())
	fmt.Printf("  Running:   %s\n", skew.RunningPath)
	if skew.PathPath != "" {
		fmt.Printf("  PATH:      %s\n", skew.PathPath)
	}
	fmt.Printf("  Installed: %s\n", skew.InstalledPath)
	fmt.Printf("  %s\n", style.Dim.Render("(via "+skew.InstalledFrom+")"))
	for _, detail := range skew.Details() {
		fmt.Printf("  %s\n", detail)
	}

	if procs, err := version.ScanSupersededProcesses(skew.InstalledPaths); err == nil && len(procs) > 0 {
		fmt.Printf("\n  %d live gt process(es) are not running the installed binary:\n", len(procs))
		for _, group := range version.GroupByExe(procs) {
			fmt.Printf("    %d × %s\n", len(group.PIDs), group.Exe)
			fmt.Printf("      pids: %s\n", joinPIDs(group.PIDs))
		}
	}
	return nil
}

func joinPIDs(pids []int) string {
	parts := make([]string, len(pids))
	for i, pid := range pids {
		parts[i] = strconv.Itoa(pid)
	}
	return strings.Join(parts, " ")
}

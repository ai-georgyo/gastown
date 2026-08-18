package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
)

var beadCreateCmd = &cobra.Command{
	Use:   "create [title] [flags]",
	Short: "Create a bead, resolving Gas Town rig aliases in --repo",
	Long: `Creates a bead, adding Gas Town rig-alias support to bd's --repo flag.

bd's own --repo takes a repository PATH. Given a bare rig name such as
"gastown" it has nothing to resolve, and rather than failing it exits 0,
prints a fabricated ID, and discards the write. This command resolves the
alias to the rig's .beads database before handing off to bd, so the bead
lands where you asked — and exits non-zero naming the valid aliases when it
cannot resolve the one you gave.

Everything else is passed through to bd create untouched:

  --repo <alias>    resolved here (gastown, hq, town, ... — this town's rigs)
  --repo <path>     left alone; bd handles paths natively
  no --repo         plain bd create in the current directory

Examples:
  gt bead create "Fix auth bug"                        # This rig (same as bd create)
  gt bead create --repo gastown "gt CLI bug"           # File in the gastown rig
  gt bead create --repo hq "Cross-rig coordination"    # File at town level
  gt bead create --repo beads "bd CLI bug" -p 1        # Any flag bd create accepts`,
	DisableFlagParsing: true,
	// This is a passthrough; on a bad --repo the caller needs the alias list,
	// not this command's two-line flag summary.
	SilenceUsage: true,
	RunE:         runBeadCreate,
}

func init() {
	beadCmd.AddCommand(beadCreateCmd)
}

func runBeadCreate(cmd *cobra.Command, args []string) error {
	// DisableFlagParsing bypasses Cobra's help handling, so do it here.
	if helped, err := checkHelpFlag(cmd, args); helped || err != nil {
		return err
	}

	createArgs, beadsDir, err := resolveBeadCreate(args, findTownRoot)
	if err != nil {
		return err
	}
	return runBdCreate(createArgs, beadsDir)
}

// resolveBeadCreate turns `gt bead create` arguments into the arguments bd
// should receive plus the .beads directory to pin it to. An empty beadsDir means
// "let bd route itself", which is correct for a plain create and for a --repo
// that already names a path.
//
// townRootFn is only consulted when an alias is actually present, so creating in
// the current rig keeps working outside a town.
func resolveBeadCreate(args []string, townRootFn func() (string, error)) ([]string, string, error) {
	argv := append([]string{"bd", "create"}, args...)

	alias, isAlias := beads.BDCreateRepoAlias(argv)
	if !isAlias {
		// No --repo, a path-like --repo, or a shape we should not second-guess:
		// bd's native handling is correct for all of them.
		return args, "", nil
	}

	townRoot, err := townRootFn()
	if err != nil {
		return nil, "", fmt.Errorf("--repo %s is a Gas Town rig alias but no town root was found (run from inside a town, or pass --repo <path>): %w", alias, err)
	}

	if _, ok := beads.ResolveRepoAliasBeadsDir(townRoot, alias); !ok {
		return nil, "", fmt.Errorf("unknown repo alias %q: no rig by that name has a beads database in %s%s\n"+
			"Refusing to create the bead — bd would have discarded it and printed a fabricated ID.",
			alias, townRoot, formatRepoAliasHint(townRoot))
	}

	rewritten, beadsDir := beads.RewriteBDCreateRepoAlias(townRoot, argv)
	if beadsDir == "" {
		return nil, "", fmt.Errorf("internal error: repo alias %q resolves but could not be rewritten out of the command line", alias)
	}

	// rewritten still carries the leading "bd" and "create".
	return rewritten[2:], beadsDir, nil
}

// buildBdCreateCmd builds the bd invocation, pinning BEADS_DIR when a target was
// resolved so the write cannot be re-routed by an inherited BEADS_DIR.
func buildBdCreateCmd(createArgs []string, beadsDir string) *exec.Cmd {
	b := BdCmd(append([]string{"create"}, createArgs...)...)
	if beadsDir != "" {
		b = b.StripBeadsDir().WithBeadsDir(beadsDir)
	}
	return b.Build()
}

func runBdCreate(createArgs []string, beadsDir string) error {
	c := buildBdCreateCmd(createArgs, beadsDir)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin

	// Mirror bd's exit code rather than collapsing every failure to 1: callers
	// script against it, and this command exists because a create that failed
	// once reported success.
	if err := c.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

// formatRepoAliasHint lists the aliases this town does accept, so the error tells
// the caller what to type instead of only what was wrong.
func formatRepoAliasHint(townRoot string) string {
	aliases := beads.RepoAliases(townRoot)
	if len(aliases) == 0 {
		return ""
	}
	return "\nKnown aliases: " + strings.Join(aliases, ", ")
}

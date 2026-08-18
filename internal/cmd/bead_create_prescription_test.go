package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestNoBDCreateRepoAliasPrescriptions is the executable form of the acceptance
// check for gt-789.
//
// `bd create --repo <rig>` exits 0, prints a fabricated ID, and discards the
// bead. Seventeen lines in the role templates gt prime injects into every agent's
// context once prescribed exactly that, so every agent that followed its own
// instructions lost beads and then quoted the fabricated IDs. This test fails if
// any prescription comes back.
//
// Lines that document the form as broken are the point of the fix, so they are
// allowed; lines that tell a reader to run it are not.
func TestNoBDCreateRepoAliasPrescriptions(t *testing.T) {
	repoRoot, guardFile := gastownRepoRootAndFile(t)

	var prescriptions []string
	for _, tree := range []string{"internal", "docs"} {
		root := filepath.Join(repoRoot, tree)
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			// This guard quotes the broken form in order to forbid it.
			if path == guardFile {
				return nil
			}
			switch filepath.Ext(path) {
			case ".md", ".tmpl", ".go", ".txt":
			default:
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				rel = path
			}
			isGo := filepath.Ext(path) == ".go"
			for i, line := range strings.Split(string(data), "\n") {
				if !strings.Contains(line, "bd create --repo") {
					continue
				}
				if strings.Contains(line, "gt bead create --repo") || describesRepoAliasAsBroken(line) {
					continue
				}
				// Go comments explain the defect to maintainers; they are not
				// injected into any agent's context and cannot prescribe anything.
				if isGo && isGoCommentLine(line) {
					continue
				}
				prescriptions = append(prescriptions,
					filepath.ToSlash(rel)+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}

	if len(prescriptions) > 0 {
		t.Errorf("found %d prescription(s) of the data-losing `bd create --repo <rig>` form.\n"+
			"Use `gt bead create --repo <rig>` instead — it resolves the alias and fails loudly when it cannot.\n%s",
			len(prescriptions), strings.Join(prescriptions, "\n"))
	}
}

// TestRoleTemplatesWarnAboutRepoAlias checks the other half: every role template
// that tells an agent how to file into another rig must also say why the raw bd
// form is not the way, since agents carry the old form in memory and habit.
func TestRoleTemplatesWarnAboutRepoAlias(t *testing.T) {
	repoRoot := gastownRepoRoot(t)
	rolesDir := filepath.Join(repoRoot, "internal", "templates", "roles")

	entries, err := os.ReadDir(rolesDir)
	if err != nil {
		t.Fatalf("read roles dir: %v", err)
	}

	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md.tmpl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(rolesDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		body := string(data)
		if !strings.Contains(body, "gt bead create --repo") {
			continue
		}
		checked++
		if !strings.Contains(body, "Never `bd create --repo <rig>`") {
			t.Errorf("%s prescribes cross-rig filing but does not warn against the raw bd form", entry.Name())
		}
	}

	if checked == 0 {
		t.Fatal("no role template prescribes cross-rig filing; the guard is checking nothing")
	}
}

// describesRepoAliasAsBroken reports whether a line mentions the form in order to
// warn about it or to name the code that compensates for it.
func isGoCommentLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*")
}

func describesRepoAliasAsBroken(line string) bool {
	for _, marker := range []string{
		"Never `bd create --repo",
		"RewriteBDCreateRepoAlias",
	} {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

// gastownRepoRootAndFile returns the repository root and this test file's path,
// so the scan can exclude the guard from its own rule.
func gastownRepoRootAndFile(t *testing.T) (string, string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..")), filepath.Clean(thisFile)
}

func gastownRepoRoot(t *testing.T) string {
	t.Helper()
	root, _ := gastownRepoRootAndFile(t)
	return root
}

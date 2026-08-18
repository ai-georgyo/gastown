package beads

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBDCreateRepoAlias(t *testing.T) {
	tests := []struct {
		name  string
		argv  []string
		alias string
		ok    bool
	}{
		{"bare alias", []string{"bd", "create", "--repo", "gastown", "title"}, "gastown", true},
		{"equals form", []string{"bd", "create", "--repo=gastown", "title"}, "gastown", true},
		{"alias after title", []string{"bd", "create", "title", "--repo", "hq"}, "hq", true},
		{"unknown name is still alias-shaped", []string{"bd", "create", "--repo", "nosuchrig"}, "nosuchrig", true},

		// Path forms belong to bd; classifying them as aliases would break it.
		{"relative path", []string{"bd", "create", "--repo", "./rig"}, "", false},
		{"absolute path", []string{"bd", "create", "--repo", "/tmp/rig"}, "", false},
		{"nested path", []string{"bd", "create", "--repo", "gastown/mayor/rig"}, "", false},
		{"home path", []string{"bd", "create", "--repo", "~/gt/gastown"}, "", false},
		{"empty value", []string{"bd", "create", "--repo", ""}, "", false},

		// Shapes we deliberately do not touch.
		{"no repo flag", []string{"bd", "create", "title"}, "", false},
		{"not create", []string{"bd", "update", "--repo", "gastown"}, "", false},
		{"two repo flags", []string{"bd", "create", "--repo", "gastown", "--repo", "hq"}, "", false},
		{"repo after terminator", []string{"bd", "create", "--", "--repo", "gastown"}, "", false},
		{"dangling repo flag", []string{"bd", "create", "--repo"}, "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			alias, ok := BDCreateRepoAlias(tc.argv)
			if ok != tc.ok || alias != tc.alias {
				t.Fatalf("BDCreateRepoAlias(%q) = (%q, %v), want (%q, %v)", tc.argv, alias, ok, tc.alias, tc.ok)
			}
		})
	}
}

// TestBDCreateRepoAliasAgreesWithRewriter pins the invariant the fix depends on:
// every argv classified as a resolvable alias must actually be rewritten, so the
// command never accepts an alias it cannot strip from the command line.
func TestBDCreateRepoAliasAgreesWithRewriter(t *testing.T) {
	townRoot := newRepoAliasTown(t)

	for _, argv := range [][]string{
		{"bd", "create", "--repo", "gastown", "title"},
		{"bd", "create", "--repo=gastown", "title"},
		{"bd", "create", "title", "--repo", "hq"},
	} {
		alias, ok := BDCreateRepoAlias(argv)
		if !ok {
			t.Fatalf("BDCreateRepoAlias(%q) did not classify as alias", argv)
		}
		if _, resolved := ResolveRepoAliasBeadsDir(townRoot, alias); !resolved {
			t.Fatalf("alias %q from %q did not resolve", alias, argv)
		}
		rewritten, beadsDir := RewriteBDCreateRepoAlias(townRoot, argv)
		if beadsDir == "" {
			t.Fatalf("RewriteBDCreateRepoAlias(%q) returned no beads dir", argv)
		}
		if _, stillThere := BDCreateRepoAlias(rewritten); stillThere {
			t.Fatalf("rewritten argv %q still carries a --repo alias", rewritten)
		}
	}
}

func TestRepoAliases(t *testing.T) {
	townRoot := newRepoAliasTown(t)

	got := RepoAliases(townRoot)
	want := []string{"gastown", "hq", "town"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RepoAliases = %v, want %v", got, want)
	}

	// Every listed alias must be one ResolveRepoAliasBeadsDir accepts, or the
	// error message would send callers to a name that also fails.
	for _, alias := range got {
		if _, ok := ResolveRepoAliasBeadsDir(townRoot, alias); !ok {
			t.Errorf("RepoAliases listed %q but it does not resolve", alias)
		}
	}

	if aliases := RepoAliases(""); aliases != nil {
		t.Errorf("RepoAliases(\"\") = %v, want nil", aliases)
	}
}

// newRepoAliasTown builds a town with one resolvable rig alias ("gastown") and
// one route whose rig has no .beads directory ("ghost"), which must not be
// offered as a valid choice.
func newRepoAliasTown(t *testing.T) string {
	t.Helper()

	townRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	townBeads := filepath.Join(townRoot, ".beads")
	rigBeads := filepath.Join(townRoot, "gastown", "mayor", "rig", ".beads")
	for _, dir := range []string{townBeads, rigBeads} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	routes := `{"prefix":"hq-","path":"."}` + "\n" +
		`{"prefix":"gt-","path":"gastown/mayor/rig"}` + "\n" +
		`{"prefix":"gh-","path":"ghost/mayor/rig"}` + "\n"
	if err := os.WriteFile(filepath.Join(townBeads, "routes.jsonl"), []byte(routes), 0644); err != nil {
		t.Fatal(err)
	}
	return townRoot
}

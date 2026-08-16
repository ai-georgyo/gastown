package beads

import "strings"

// Agent identity normalization for bead assignees.
//
// Gas Town wrote agent identity two different ways. Town-level singletons
// sometimes carried a trailing slash ("deacon/", "mayor/") and sometimes did
// not ("deacon", "mayor"). Writers and readers disagreed, so records that
// existed became invisible to the code looking for them:
//
//   - gt patrol report armed the next patrol wisp with assignee "deacon" while
//     gt hook queried for "deacon/", so the deacon's own hook looked empty and
//     the patrol loop stalled silently (hq-j5v defect 1).
//   - gt mail send wrote mail beads with assignee "deacon/" while BD_ACTOR is
//     "deacon", so bd's ownership check refused to let the deacon archive its
//     own mail (hq-516).
//
// CANONICAL FORM: no trailing slash ("deacon", "mayor").
//
// The bare form is canonical because it is the one identity gastown does not
// control unilaterally. bd compares a bead's assignee against the actor when
// deciding ownership, and the actor comes from BD_ACTOR, which
// config.AgentEnv sets to the bare role name. session.AgentIdentity.Address()
// agrees. Any slashed assignee is therefore un-ownable by the very agent it
// names — the trailing slash cannot be made canonical without either forking
// bd's ownership rule or rewriting BD_ACTOR everywhere, including in history
// already recorded in created_by.
//
// The normalization is applied to the bd argv itself (see NormalizeAssigneeArgs)
// rather than exposed as a helper callers must remember to call, so a caller
// that hand-builds "deacon/" still lands the canonical form on disk.
//
// Mail addresses are a separate namespace and are deliberately left alone:
// internal/mail keeps "deacon/" as its address form ("gastown/nux" rather than
// "gastown/polecats/nux" for polecats, too). Only the value that reaches bd's
// assignee column is normalized.

// townLevelSingletons are the agent identities that were historically written
// with a trailing slash. Rig-level addresses ("gastown/witness") never carried
// one, so they need no legacy variant.
var townLevelSingletons = map[string]bool{
	"mayor":  true,
	"deacon": true,
}

// CanonicalAssignee returns the canonical form of a bead assignee: trimmed,
// with any trailing slashes removed.
//
// Values that are not agent addresses (mail fan-out pseudo-assignees like
// "queue:build" or "channel:ops", and the empty unassign value) pass through
// unchanged because they carry no trailing slash to begin with. An input that
// is nothing but slashes is returned unchanged rather than emptied, so a
// malformed value can never silently turn an assignee filter into "match all".
func CanonicalAssignee(assignee string) string {
	trimmed := strings.TrimSpace(assignee)
	canonical := strings.TrimRight(trimmed, "/")
	if canonical == "" {
		return assignee
	}
	return canonical
}

// SameAssignee reports whether two assignee values name the same agent,
// ignoring the trailing-slash difference. Use it instead of == whenever a
// stored assignee is compared against an identity built in code: the stored
// value may predate normalization.
func SameAssignee(a, b string) bool {
	return CanonicalAssignee(a) == CanonicalAssignee(b)
}

// AssigneeQueryVariants returns every stored form a reader must match to find
// work assigned to the given agent: the canonical form plus, for town-level
// singletons, the legacy trailing-slash form that older records still carry.
//
// Readers need this because normalizing writes does not rewrite rows already
// in the database. Once no "deacon/" rows remain, the legacy variant costs one
// extra term in a query and nothing else.
func AssigneeQueryVariants(assignee string) []string {
	canonical := CanonicalAssignee(assignee)
	if canonical == "" {
		return nil
	}
	if townLevelSingletons[canonical] {
		return []string{canonical, canonical + "/"}
	}
	return []string{canonical}
}

// listAcrossAssigneeVariants runs list once per stored assignee form and merges
// the results, preserving first-seen order and dropping duplicate IDs.
func (b *Beads) listAcrossAssigneeVariants(opts ListOptions, variants []string, list func(ListOptions) ([]*Issue, error)) ([]*Issue, error) {
	limit := opts.Limit
	seen := make(map[string]bool)
	var merged []*Issue

	for _, variant := range variants {
		variantOpts := opts
		variantOpts.Assignee = variant
		variantOpts.Limit = 0
		issues, err := list(variantOpts)
		if err != nil {
			return nil, err
		}
		for _, issue := range issues {
			if issue == nil || seen[issue.ID] {
				continue
			}
			seen[issue.ID] = true
			merged = append(merged, issue)
		}
	}

	if limit > 0 && len(merged) > limit {
		return merged[:limit], nil
	}
	return merged, nil
}

// NormalizeAssigneeArgs canonicalizes the value of every --assignee flag in a
// bd argv, in both the "--assignee=value" and "--assignee value" spellings.
//
// Only mutating commands are rewritten. Read commands are left exactly as the
// caller wrote them so that a reader deliberately querying the legacy
// trailing-slash form (see AssigneeQueryVariants) still finds legacy rows.
//
// The returned slice is the input slice when nothing needed changing.
func NormalizeAssigneeArgs(args []string) []string {
	if len(args) == 0 || ArgsAreReadOnly(args) {
		return args
	}

	var out []string
	for i := 0; i < len(args); i++ {
		arg := args[i]

		// "--assignee value": normalize the following argument.
		if arg == "--assignee" && i+1 < len(args) {
			canonical := CanonicalAssignee(args[i+1])
			if canonical != args[i+1] {
				if out == nil {
					out = append(out, args[:i+1]...)
				}
				out = append(out, canonical)
				i++
				continue
			}
			if out != nil {
				out = append(out, arg, args[i+1])
			}
			i++
			continue
		}

		// "--assignee=value": normalize the inline value.
		if value, ok := strings.CutPrefix(arg, "--assignee="); ok {
			canonical := CanonicalAssignee(value)
			if canonical != value {
				if out == nil {
					out = append(out, args[:i]...)
				}
				out = append(out, "--assignee="+canonical)
				continue
			}
		}

		if out != nil {
			out = append(out, arg)
		}
	}

	if out == nil {
		return args
	}
	return out
}

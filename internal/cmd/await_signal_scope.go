package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The activity feed at <town>/.events.jsonl is town-wide: every rig's slings,
// mail, nudges, session starts and deaths land in the same file. await-signal
// woke on any line, so a rig agent returned on other rigs' traffic, its idle
// counter never advanced, and the exponential backoff and abbreviated patrol
// mode that depend on that counter were unreachable in a busy town (gt-qc1).
//
// rigScopeMatcher decides whether one event line concerns a given rig. It is
// deliberately asymmetric:
//
//   - Any evidence the event is MINE wakes the agent.
//   - Only clear evidence that the event belongs to ANOTHER rig suppresses it.
//   - An event that names no rig at all (town halt, deacon-to-boot nudge,
//     mass death) still wakes: town-level activity is every agent's business,
//     and a missed wake costs more than a spurious one.
//
// Rig scoping is opt-in via --rig. Town-level agents (the deacon) pass no rig
// and keep the old town-wide behavior.
type rigScopeMatcher struct {
	rig       string
	knownRigs map[string]struct{}
}

// townLevelAddressPrefixes are address prefixes that name a town-level agent
// rather than a rig. "mayor/", "deacon/", "convoy/hq-cv-x" and friends are not
// rig references, so they neither match nor rule out any rig.
var townLevelAddressPrefixes = map[string]struct{}{
	"mayor":   {},
	"deacon":  {},
	"boot":    {},
	"gt":      {},
	"hq":      {},
	"convoy":  {},
	"town":    {},
	"daemon":  {},
	"channel": {},
}

// eventRigFields are the payload keys that can carry a rig name or a
// rig-qualified agent address. "rig" is the authoritative one when present and
// correct; the address fields cover the events that carry no rig at all.
var eventRigFields = []string{"rig", "to", "target", "agent", "role", "from"}

// newRigScopeMatcher builds a matcher for rigName. Returns nil (match
// everything) when no rig scope was requested.
func newRigScopeMatcher(townRoot, rigName string) *rigScopeMatcher {
	rigName = strings.TrimSpace(rigName)
	if rigName == "" {
		return nil
	}
	return &rigScopeMatcher{
		rig:       rigName,
		knownRigs: discoverRigNames(townRoot),
	}
}

// discoverRigNames lists the rigs in a town. A directory is a rig when it holds
// a config.json — the same test detectRigFromPath uses.
//
// The set is needed to tell a bare rig name from a bare token that merely looks
// like one: payload["rig"] is a real rig name on nudge and spawn events, but
// escalate passes a bead ID in that field, and session_death uses a tmux
// session name as its actor. Only names that are actually rigs are allowed to
// rule an event out.
func discoverRigNames(townRoot string) map[string]struct{} {
	rigs := make(map[string]struct{})
	if townRoot == "" {
		return rigs
	}
	entries, err := os.ReadDir(townRoot)
	if err != nil {
		return rigs
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(townRoot, entry.Name(), "config.json")); err == nil {
			rigs[entry.Name()] = struct{}{}
		}
	}
	return rigs
}

// matches reports whether an event line should wake an agent scoped to m.rig.
// A nil matcher matches everything (town-wide, the pre-gt-qc1 behavior).
//
// Unparseable lines match: a filter that swallows events it cannot read would
// turn a feed-format change into a silently deaf agent.
func (m *rigScopeMatcher) matches(line string) bool {
	if m == nil || m.rig == "" {
		return true
	}

	var event struct {
		Actor   string                 `json:"actor"`
		Rig     string                 `json:"rig"`
		Payload map[string]interface{} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return true
	}

	candidates := []string{event.Actor, event.Rig}
	for _, field := range eventRigFields {
		if v, ok := event.Payload[field].(string); ok {
			candidates = append(candidates, v)
		}
	}

	foreign := false
	for _, candidate := range candidates {
		switch m.classify(candidate) {
		case rigRefMine:
			return true
		case rigRefForeign:
			foreign = true
		}
	}

	// No rig named anywhere: town-level activity, wake.
	return !foreign
}

type rigRef int

const (
	rigRefNone rigRef = iota // not a rig reference
	rigRefMine
	rigRefForeign
)

// classify decides what a single candidate string says about rig ownership.
//
// A qualified address ("gastown/witness", "myr/polecats/mycat") names its rig
// in the first segment, and that segment is trusted even for a rig that no
// longer exists on disk — a stale rig's traffic is still not mine. A bare token
// ("gastown", "hq-wisp-g0ei", "gt-mycat") is trusted only when it is a rig that
// actually exists, because several events put non-rig values in these fields.
func (m *rigScopeMatcher) classify(candidate string) rigRef {
	candidate = strings.Trim(strings.TrimSpace(candidate), "/")
	if candidate == "" {
		return rigRefNone
	}

	qualified := strings.Contains(candidate, "/")
	name := candidate
	if qualified {
		name = candidate[:strings.Index(candidate, "/")]
	}

	if name == m.rig {
		return rigRefMine
	}
	if _, reserved := townLevelAddressPrefixes[name]; reserved {
		return rigRefNone
	}
	if qualified {
		return rigRefForeign
	}
	if _, known := m.knownRigs[name]; known {
		return rigRefForeign
	}
	return rigRefNone
}

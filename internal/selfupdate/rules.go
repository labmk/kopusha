package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RuleAction is what an update does to one file in parsers.d/.
type RuleAction string

const (
	// RuleAdd installs a rule the running binary never shipped and the
	// user does not have. This is how a release adds a log format to an
	// existing install.
	RuleAdd RuleAction = "add"
	// RuleReplace overwrites a shipped rule the user has not touched.
	RuleReplace RuleAction = "replace"
	// RuleDelete removes a shipped, untouched rule that the new release
	// withdrew. Leaving it behind is not inert: parsers.d/ loads in
	// lexicographic order and the first match wins, so a stale file can
	// shadow the rule meant to supersede it.
	RuleDelete RuleAction = "delete"
	// RuleKeep leaves a file exactly as it is. Every keep carries a
	// reason, because a keep is the case the user needs told about.
	RuleKeep RuleAction = "keep"
)

// RuleChange is one file's outcome, decided before anything is written.
type RuleChange struct {
	Name   string     `json:"name"`
	Action RuleAction `json:"action"`
	// Reason is set on every RuleKeep and is written for a person to
	// read: "you edited it", not "hash mismatch".
	Reason string `json:"reason,omitempty"`

	// content is the incoming file for add and replace. Unexported: the
	// plan is served to the UI as JSON and rule bodies do not belong in
	// it.
	content []byte
}

// RulePlan is the full set of decisions for parsers.d/.
type RulePlan struct {
	Changes []RuleChange `json:"changes"`
	// Frozen is true when the running binary carries no manifest, in
	// which case nothing can be classified and nothing is touched.
	Frozen bool `json:"frozen"`
}

// Kept returns the names the update will not touch, with reasons, for the
// report the user sees afterwards. Reporting this is the whole reason the
// manifest exists: someone who edited a shipped rule to fix a regex would
// otherwise never receive upstream fixes to it and have no way to find
// out.
func (p RulePlan) Kept() []RuleChange {
	var out []RuleChange
	for _, c := range p.Changes {
		if c.Action == RuleKeep {
			out = append(out, c)
		}
	}
	return out
}

// Counts summarises the plan as action → number, for a one-line message.
func (p RulePlan) Counts() map[RuleAction]int {
	m := map[RuleAction]int{}
	for _, c := range p.Changes {
		m[c.Action]++
	}
	return m
}

// PlanRules decides what happens to every rule file, comparing what is on
// disk against what this binary shipped with — never against the incoming
// release. The running binary is the only thing that knows what arrived
// with it, which is why the manifest is embedded rather than written to
// the install directory.
//
// incoming maps a rule's base name to its contents in the new release.
func (u *Updater) PlanRules(incoming map[string][]byte) (RulePlan, error) {
	var plan RulePlan

	// A binary built before the manifest existed cannot tell a rule the
	// user wrote from one that shipped. The only safe reading of that is
	// to touch nothing: silently overwriting someone's rules is the
	// failure this whole design exists to prevent.
	if len(u.Shipped) == 0 {
		plan.Frozen = true
		return plan, nil
	}

	onDisk, err := hashRules(u.RulesDir)
	if err != nil {
		return plan, err
	}

	seen := map[string]bool{}

	for name, want := range u.Shipped {
		seen[name] = true
		got, present := onDisk[name]
		newContent, offered := incoming[name]

		switch {
		case !present:
			// Deleting a rule is how you disable it. Restoring it would
			// undo a deliberate act.
			plan.Changes = append(plan.Changes, RuleChange{
				Name: name, Action: RuleKeep,
				Reason: "you deleted it, so it stays deleted",
			})
		case got != want:
			plan.Changes = append(plan.Changes, RuleChange{
				Name: name, Action: RuleKeep,
				Reason: "you edited it",
			})
		case !offered:
			// Untouched and withdrawn upstream.
			plan.Changes = append(plan.Changes, RuleChange{
				Name: name, Action: RuleDelete,
			})
		case hashBytes(newContent) == got:
			// Identical in the new release. Not a change at all, and
			// listing it as one would bury the changes that matter.
		default:
			plan.Changes = append(plan.Changes, RuleChange{
				Name: name, Action: RuleReplace, content: newContent,
			})
		}
	}

	for name, content := range incoming {
		if seen[name] {
			continue
		}
		if _, present := onDisk[name]; present {
			// A rule the user wrote that happens to share a name with a
			// new shipped rule. Their file wins — this is the one case
			// where an update would destroy work that was never ours.
			plan.Changes = append(plan.Changes, RuleChange{
				Name: name, Action: RuleKeep,
				Reason: "it is your own rule, and the release adds one with the same name",
			})
			continue
		}
		plan.Changes = append(plan.Changes, RuleChange{
			Name: name, Action: RuleAdd, content: content,
		})
	}

	// Files on disk that neither shipped nor arrived are the user's own
	// and are not mentioned: an update that says nothing about them is
	// exactly right.

	sort.Slice(plan.Changes, func(i, j int) bool {
		return plan.Changes[i].Name < plan.Changes[j].Name
	})
	return plan, nil
}

// ApplyRules writes a plan to disk. It is called only after the binary has
// been replaced: rules describe formats the new binary understands, so
// applying them against the old one would be the wrong way round if the
// update then failed.
func (u *Updater) ApplyRules(plan RulePlan) error {
	if plan.Frozen {
		return nil
	}
	if err := os.MkdirAll(u.RulesDir, 0o755); err != nil {
		return fmt.Errorf("parsers.d: %w", err)
	}
	for _, c := range plan.Changes {
		path := filepath.Join(u.RulesDir, c.Name)
		switch c.Action {
		case RuleAdd, RuleReplace:
			if err := os.WriteFile(path, c.content, 0o644); err != nil {
				return fmt.Errorf("parsers.d: write %s: %w", c.Name, err)
			}
		case RuleDelete:
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("parsers.d: remove %s: %w", c.Name, err)
			}
		}
	}
	return nil
}

// hashRules hashes every rule file in dir. A missing directory is not an
// error — running without parsers.d/ is supported, since NDJSON and EVTX
// need no rules.
func hashRules(dir string) (map[string]string, error) {
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("parsers.d: read %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !isRuleFile(e.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("parsers.d: read %s: %w", e.Name(), err)
		}
		out[e.Name()] = hashBytes(data)
	}
	return out, nil
}

func isRuleFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

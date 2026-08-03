package selfupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRules builds an Updater over a temporary parsers.d/ containing the
// given files, with shipped recording the hashes the binary was built
// with. shipped names a subset (or superset) of what is written.
func newRules(t *testing.T, onDisk map[string]string, shippedAs map[string]string) *Updater {
	t.Helper()
	dir := t.TempDir()
	for name, body := range onDisk {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	shipped := map[string]string{}
	for name, body := range shippedAs {
		shipped[name] = hashBytes([]byte(body))
	}
	return &Updater{RulesDir: dir, Shipped: shipped}
}

func actions(p RulePlan) map[string]RuleAction {
	m := map[string]RuleAction{}
	for _, c := range p.Changes {
		m[c.Name] = c.Action
	}
	return m
}

func TestPlanRulesFollowsThePolicy(t *testing.T) {
	// The six rows of the decided table in docs/SELF_UPDATE_PROPOSAL.md,
	// plus the "new rule arrives" case that makes the whole mechanism
	// worth having.
	u := newRules(t,
		map[string]string{
			"10-untouched.yaml":   "shipped body",
			"20-edited.yaml":      "the user fixed a regex here",
			"30-withdrawn.yaml":   "shipped body",
			"40-edited-gone.yaml": "the user fixed a regex here too",
			"90-mine.yaml":        "a rule the user wrote",
			// 50-deleted.yaml shipped and was deleted: absent on disk.
		},
		map[string]string{
			"10-untouched.yaml":   "shipped body",
			"20-edited.yaml":      "shipped body",
			"30-withdrawn.yaml":   "shipped body",
			"40-edited-gone.yaml": "shipped body",
			"50-deleted.yaml":     "shipped body",
		},
	)

	incoming := map[string][]byte{
		"10-untouched.yaml": []byte("a newer body"),
		"20-edited.yaml":    []byte("a newer body"),
		"50-deleted.yaml":   []byte("a newer body"),
		"60-new.yaml":       []byte("a format the release adds"),
		// 30-withdrawn.yaml and 40-edited-gone.yaml are gone upstream.
	}

	plan, err := u.PlanRules(incoming)
	if err != nil {
		t.Fatal(err)
	}
	got := actions(plan)

	want := map[string]RuleAction{
		"10-untouched.yaml":   RuleReplace, // untouched: the point of the feature
		"20-edited.yaml":      RuleKeep,    // user edited it
		"30-withdrawn.yaml":   RuleDelete,  // untouched and withdrawn: would shadow
		"40-edited-gone.yaml": RuleKeep,    // edited and withdrawn: their call
		"50-deleted.yaml":     RuleKeep,    // deleted on purpose, stays deleted
		"60-new.yaml":         RuleAdd,     // new format reaches an old install
	}
	for name, action := range want {
		if got[name] != action {
			t.Errorf("%s: action = %q, want %q", name, got[name], action)
		}
	}
	// A rule the user wrote that the release says nothing about is not
	// mentioned at all.
	if _, mentioned := got["90-mine.yaml"]; mentioned {
		t.Errorf("90-mine.yaml should not appear in the plan: %v", got["90-mine.yaml"])
	}
}

// Every keep has to explain itself: a silent keep is how someone never
// finds out they stopped receiving fixes to a rule they edited.
func TestPlanRulesExplainsEveryKeep(t *testing.T) {
	u := newRules(t,
		map[string]string{"20-edited.yaml": "edited"},
		map[string]string{"20-edited.yaml": "shipped", "50-deleted.yaml": "shipped"},
	)
	plan, err := u.PlanRules(map[string][]byte{
		"20-edited.yaml":  []byte("newer"),
		"50-deleted.yaml": []byte("newer"),
	})
	if err != nil {
		t.Fatal(err)
	}
	kept := plan.Kept()
	if len(kept) != 2 {
		t.Fatalf("kept %d rules, want 2", len(kept))
	}
	for _, c := range kept {
		if c.Reason == "" {
			t.Errorf("%s was kept without a reason", c.Name)
		}
	}
}

// A rule the user wrote must not be overwritten by a shipped rule that
// arrives later under the same name.
func TestPlanRulesDoesNotOverwriteAUsersOwnRule(t *testing.T) {
	u := newRules(t,
		map[string]string{"60-new.yaml": "the user got there first"},
		map[string]string{"10-shipped.yaml": "shipped"},
	)
	plan, err := u.PlanRules(map[string][]byte{"60-new.yaml": []byte("the release's version")})
	if err != nil {
		t.Fatal(err)
	}
	if a := actions(plan)["60-new.yaml"]; a != RuleKeep {
		t.Fatalf("action = %q, want keep", a)
	}
	if err := u.ApplyRules(plan); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(u.RulesDir, "60-new.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "the user got there first" {
		t.Errorf("the user's rule was overwritten: %q", body)
	}
}

// A rule that is byte-identical in the new release is not a change, and
// listing it as one would bury the changes that matter.
func TestPlanRulesIgnoresIdenticalRules(t *testing.T) {
	u := newRules(t,
		map[string]string{"10-same.yaml": "shipped body"},
		map[string]string{"10-same.yaml": "shipped body"},
	)
	plan, err := u.PlanRules(map[string][]byte{"10-same.yaml": []byte("shipped body")})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 0 {
		t.Errorf("plan = %+v, want no changes", plan.Changes)
	}
}

// A binary built before the manifest existed cannot tell a user's rule
// from a shipped one, so it must touch nothing rather than guess.
func TestPlanRulesFreezesWithoutAManifest(t *testing.T) {
	u := newRules(t, map[string]string{"10-something.yaml": "body"}, nil)
	plan, err := u.PlanRules(map[string][]byte{"10-something.yaml": []byte("newer")})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Frozen || len(plan.Changes) != 0 {
		t.Fatalf("plan = %+v, want frozen and empty", plan)
	}
	if err := u.ApplyRules(plan); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(u.RulesDir, "10-something.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "body" {
		t.Errorf("a frozen plan modified the disk: %q", body)
	}
}

// Running without parsers.d/ at all is supported — NDJSON and EVTX need
// no rules — so an absent directory is a state, not a failure.
func TestPlanRulesHandlesAMissingDirectory(t *testing.T) {
	u := newRules(t, nil, map[string]string{"10-shipped.yaml": "shipped"})
	u.RulesDir = filepath.Join(u.RulesDir, "not-created")

	plan, err := u.PlanRules(map[string][]byte{"10-shipped.yaml": []byte("newer")})
	if err != nil {
		t.Fatal(err)
	}
	if a := actions(plan)["10-shipped.yaml"]; a != RuleKeep {
		t.Errorf("action = %q, want keep (deleting a rule disables it)", a)
	}
}

func TestApplyRulesWritesThePlan(t *testing.T) {
	u := newRules(t,
		map[string]string{
			"10-untouched.yaml": "shipped body",
			"30-withdrawn.yaml": "shipped body",
		},
		map[string]string{
			"10-untouched.yaml": "shipped body",
			"30-withdrawn.yaml": "shipped body",
		},
	)
	plan, err := u.PlanRules(map[string][]byte{
		"10-untouched.yaml": []byte("a newer body"),
		"60-new.yaml":       []byte("a new format"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := u.ApplyRules(plan); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		"10-untouched.yaml": "a newer body",
		"60-new.yaml":       "a new format",
	} {
		body, err := os.ReadFile(filepath.Join(u.RulesDir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(body) != want {
			t.Errorf("%s = %q, want %q", name, body, want)
		}
	}
	if _, err := os.Stat(filepath.Join(u.RulesDir, "30-withdrawn.yaml")); !os.IsNotExist(err) {
		t.Errorf("30-withdrawn.yaml survived: %v", err)
	}
}

// The plan is served to the UI as JSON; rule bodies are not part of that.
func TestRuleChangeKeepsBodiesOutOfTheAPI(t *testing.T) {
	u := newRules(t, nil, map[string]string{"10-shipped.yaml": "shipped"})
	plan, err := u.PlanRules(map[string][]byte{"60-new.yaml": []byte("secret-ish rule body")})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) == 0 {
		t.Fatal("expected a change")
	}
	// The content field is unexported, so encoding cannot leak it. Assert
	// on the rendered form rather than trusting that to stay true.
	if strings.Contains(renderJSON(t, plan), "secret-ish") {
		t.Error("rule bodies leaked into the serialised plan")
	}
}

func renderJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

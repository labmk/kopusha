package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// SamplePlan is what an update does to samples/.
//
// # Why this is not the parsers.d/ policy
//
// parsers.d/ gets the careful treatment — an embedded manifest, replace
// only what is byte-identical to what shipped, report every skip —
// because those files are configuration the user is invited to edit, and
// losing an edited rule would lose real work.
//
// samples/ is the opposite kind of thing. It is inert demo data: the
// binary never reads it, deleting it changes nothing, and nobody
// hand-tunes a synthetic log. So "did you edit this?" is a distinction
// without a difference here, and carrying a second manifest to answer it
// would be machinery bought for nothing.
//
// The rules are therefore short: shipped samples are written over,
// anything else in the folder is left strictly alone, and the folder is
// created when it is missing.
//
// That last clause is deliberate, and it is the whole reason this exists.
// An install that self-updated from a version predating samples/ has no
// such folder, and honouring "absent means you deleted it" — which is
// exactly right for a parser rule — would mean those installs never
// receive samples at all. The cost of being wrong the other way is that
// someone who deleted the folder sees 130 KB of inert files return, and
// the update report says so.
type SamplePlan struct {
	// Write lists the sample files the update will create or overwrite.
	Write []string `json:"write"`
	// Created is true when samples/ did not exist and will be made.
	Created bool `json:"created"`
}

// PlanSamples decides what happens to samples/. It never inspects what is
// already there beyond noticing whether the directory exists, because no
// outcome depends on the contents: shipped names are written, everything
// else is untouched.
func (u *Updater) PlanSamples(incoming map[string][]byte) SamplePlan {
	var plan SamplePlan
	if u.SamplesDir == "" || len(incoming) == 0 {
		return plan
	}
	for name := range incoming {
		plan.Write = append(plan.Write, name)
	}
	sort.Strings(plan.Write)

	if fi, err := os.Stat(u.SamplesDir); err != nil || !fi.IsDir() {
		plan.Created = true
	}
	return plan
}

// ApplySamples writes the plan. A failure here is reported but is not
// grounds for undoing an update: the binary and the parser rules are what
// make the tool work, and a missing sample log is a missing convenience.
func (u *Updater) ApplySamples(plan SamplePlan, incoming map[string][]byte) error {
	if len(plan.Write) == 0 {
		return nil
	}
	if err := os.MkdirAll(u.SamplesDir, 0o755); err != nil {
		return fmt.Errorf("samples: %w", err)
	}
	for _, name := range plan.Write {
		body, ok := incoming[name]
		if !ok {
			continue
		}
		if err := os.WriteFile(filepath.Join(u.SamplesDir, name), body, 0o644); err != nil {
			return fmt.Errorf("samples: write %s: %w", name, err)
		}
	}
	return nil
}

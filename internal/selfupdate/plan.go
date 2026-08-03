package selfupdate

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotWritable means the install directory cannot be written. On
// locked-down Windows this is the common case rather than the exception,
// so it is checked before the download rather than after it.
var ErrNotWritable = errors.New("the install directory is not writable")

// Plan is everything an update would do, computed before a single byte is
// written. It is served to the UI so the user presses the button knowing
// what happens — particularly which of their parser rules will be left
// alone.
type Plan struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Asset string `json:"asset"`
	Size  int64  `json:"size"`

	// Attestation is the verified provenance of the downloaded archive.
	Attestation Attestation `json:"attestation"`
	// Verification names what was actually checked, so the UI can say it
	// precisely instead of implying a signature check that did not
	// happen. See Verify.
	Verification string `json:"verification"`

	Rules      RulePlan `json:"rules"`
	InstallDir string   `json:"install_dir"`

	// archive is the verified zip, carried from Prepare to Apply so the
	// bytes that were checked are the bytes that get installed. Passing a
	// path instead would leave a window in which the file could change.
	archive []byte
}

// Result is what an update did, for the report shown afterwards.
type Result struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Backup string `json:"backup"`

	RulesAdded    []string     `json:"rules_added,omitempty"`
	RulesReplaced []string     `json:"rules_replaced,omitempty"`
	RulesDeleted  []string     `json:"rules_deleted,omitempty"`
	RulesKept     []RuleChange `json:"rules_kept,omitempty"`
	// RulesFrozen is true when the running binary carried no manifest and
	// parsers.d/ was therefore left entirely alone.
	RulesFrozen bool `json:"rules_frozen"`
}

// Prepare downloads the release, verifies it, and works out what applying
// it would do. Nothing is written to the install directory.
//
// The order is the contract: check writability, download, verify, and only
// then read the archive. Verifying before opening means a hostile zip is
// never parsed at all, and checking writability first means the common
// Windows failure costs nothing rather than 60 MB.
func (u *Updater) Prepare(ctx context.Context, version string) (*Plan, error) {
	if !writable(u.InstallDir) {
		return nil, fmt.Errorf("%w: %s", ErrNotWritable, u.InstallDir)
	}

	asset := AssetName(version)
	data, err := u.download(ctx, version, asset)
	if err != nil {
		return nil, err
	}

	att, err := u.Verify(ctx, data, asset, version)
	if err != nil {
		return nil, err
	}

	a, err := openArchive(data)
	if err != nil {
		return nil, err
	}
	// Fail here rather than after the old binary has been moved aside.
	if _, ok, err := a.read(binaryName()); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("the archive contains no %s", binaryName())
	}

	incoming, err := a.rules()
	if err != nil {
		return nil, err
	}
	rulePlan, err := u.PlanRules(incoming)
	if err != nil {
		return nil, err
	}

	return &Plan{
		From:        u.Current,
		To:          version,
		Asset:       asset,
		Size:        int64(len(data)),
		Attestation: att,
		Verification: "GitHub holds a build attestation for these exact bytes, " +
			"naming this repository, the release workflow and the tag. The " +
			"Sigstore signature on that attestation was not itself checked — " +
			"run `gh attestation verify` for that.",
		Rules:      rulePlan,
		InstallDir: u.InstallDir,
		archive:    data,
	}, nil
}

// Apply installs a prepared plan: the binary first, then the parser rules.
//
// That order matters. Rules describe formats the *new* binary understands,
// so writing them before the binary would leave an old binary reading new
// rules if the swap then failed. Applied afterwards, a failure to install
// the binary leaves parsers.d/ untouched.
func (u *Updater) Apply(plan *Plan) (*Result, error) {
	if plan == nil || len(plan.archive) == 0 {
		return nil, errors.New("apply: the plan carries no verified archive")
	}

	a, err := openArchive(plan.archive)
	if err != nil {
		return nil, err
	}
	bin, ok, err := a.read(binaryName())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("the archive contains no %s", binaryName())
	}

	backup, err := replaceBinary(u.ExePath, bin)
	if err != nil {
		return nil, err
	}

	if err := u.ApplyRules(plan.Rules); err != nil {
		// The binary is already the new one. Rules are advisory — a rule
		// that failed to write means one format is not recognised, not a
		// broken install — so this is reported rather than rolled back.
		return nil, fmt.Errorf("the new binary is installed, but parser rules could not be updated: %w", err)
	}

	res := &Result{
		From:        plan.From,
		To:          plan.To,
		Backup:      backup,
		RulesKept:   plan.Rules.Kept(),
		RulesFrozen: plan.Rules.Frozen,
	}
	for _, c := range plan.Rules.Changes {
		switch c.Action {
		case RuleAdd:
			res.RulesAdded = append(res.RulesAdded, c.Name)
		case RuleReplace:
			res.RulesReplaced = append(res.RulesReplaced, c.Name)
		case RuleDelete:
			res.RulesDeleted = append(res.RulesDeleted, c.Name)
		}
	}
	return res, nil
}

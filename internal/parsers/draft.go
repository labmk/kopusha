// Package parsers owns the parsers.d directory.
//
// It has two jobs that the ingest layer deliberately does not:
//
//   - It builds the loader Registry from the rules on disk, and rebuilds
//     it on demand, so a rule authored in the UI takes effect without a
//     restart.
//   - It is the write path for new rules: infer a candidate rule from
//     sample lines (suggest.go), run it through the real adapter to show
//     what it would produce (preview.go), and write it out as YAML.
//
// The split matters. internal/ingest reads rules and knows nothing about
// where they came from; this package decides what is on disk. Keeping
// the write path out of ingest means the parsing code has no filesystem
// authority, which is the right shape for the component that handles
// untrusted input.
package parsers

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/labmk/kopusha/internal/ingest"
)

// Sub is one regex substitution applied to the captured timestamp before
// it is parsed. Mirrors the `ts_regex_subs` entries the line adapter
// compiles.
type Sub struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
}

// Draft is a line-format rule being authored: the same fields the line
// adapter compiles, in a shape that survives a round trip through JSON
// and back from a half-finished form.
//
// Only the `line` family is draftable. Block and XML rules need a
// separator or a row element rather than a single regex over one line,
// and inferring those from a pasted sample is a different problem —
// see docs/PARSER-RULES.md.
type Draft struct {
	Name           string `json:"name"`
	Priority       int    `json:"priority"`
	Parse          string `json:"parse"`
	TsLayout       string `json:"ts_layout"`
	TsAssumeTZ     string `json:"ts_assume_tz,omitempty"`
	TsUseMtimeDate bool   `json:"ts_use_mtime_date,omitempty"`
	TsUseMtimeYear bool   `json:"ts_use_mtime_year,omitempty"`
	TsRegexSubs    []Sub  `json:"ts_regex_subs,omitempty"`
	MessageField   string `json:"message_field,omitempty"`

	// Sample is the text the rule was inferred from. Written into the
	// saved file as a header comment, because the single most useful
	// thing a rule file can carry is an example of what it parses —
	// every shipped rule in parsers.d/ starts with one.
	Sample string `json:"sample,omitempty"`

	// Warnings are things the operator should look at before saving:
	// an ambiguous date order, a timestamp the inference could not
	// place. Advisory only — never a reason to refuse a save.
	Warnings []string `json:"warnings,omitempty"`
}

// defaultPriority sits below every rule shipped in parsers.d/ (80-100).
//
// A user's own rule losing a tie to a shipped rule is the safe default:
// the shipped rules are narrow and a file that matches one is almost
// certainly that format. A rule that needs to win says so by raising
// its priority, which is a deliberate act with a visible number.
const defaultPriority = 50

var nameRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Slug normalizes a user-supplied rule name into something safe to use
// as both a rule name and a filename: lower case, ASCII alphanumerics,
// single hyphens between runs.
//
// This is a security boundary, not a tidiness pass. The name arrives
// from a browser and becomes a path component, so anything that could
// escape parsers.d/ — a separator, a dot segment, a drive letter, a NUL
// — has to be gone before the value reaches filepath.Join. Building the
// slug from an allow-list rather than stripping a deny-list is what
// makes that true for inputs nobody thought of.
func Slug(name string) (string, error) {
	var b strings.Builder
	lastHyphen := true // leading hyphens are suppressed
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "", fmt.Errorf("rule name %q contains no letters or digits", name)
	}
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	if !nameRe.MatchString(s) {
		return "", fmt.Errorf("rule name %q could not be normalized", name)
	}
	return s, nil
}

// Validate reports whether the draft would compile as a line rule. It
// runs the same checks the adapter does, plus the ones the adapter
// cannot: that the regex actually names a `ts` group, and that the
// layout is a plausible Go time layout rather than a strftime string
// (`%Y-%m-%d`), which is the mistake everyone makes first.
//
// The name is not checked here. A draft is previewable long before it
// is nameable — the operator pastes lines, watches the table, and names
// the thing last — so the name is Save's business, not the preview's.
func (d *Draft) Validate() error {
	if strings.TrimSpace(d.Parse) == "" {
		return fmt.Errorf("the parse regex is required")
	}
	re, err := regexp.Compile(d.Parse)
	if err != nil {
		return fmt.Errorf("parse regex: %w", err)
	}
	if re.SubexpIndex("ts") < 0 {
		return fmt.Errorf("the parse regex must capture the timestamp as (?P<ts>...)")
	}
	if strings.TrimSpace(d.TsLayout) == "" {
		return fmt.Errorf("ts_layout is required")
	}
	if strings.Contains(d.TsLayout, "%") {
		return fmt.Errorf("ts_layout %q looks like a strftime pattern; Go layouts spell out a reference time, e.g. 2006-01-02 15:04:05", d.TsLayout)
	}
	if d.TsUseMtimeDate && d.TsUseMtimeYear {
		return fmt.Errorf("ts_use_mtime_date and ts_use_mtime_year cannot both be set")
	}
	if d.TsAssumeTZ != "" {
		if _, err := time.LoadLocation(d.TsAssumeTZ); err != nil {
			return fmt.Errorf("ts_assume_tz: %w", err)
		}
	}
	for i, s := range d.TsRegexSubs {
		if strings.TrimSpace(s.Pattern) == "" {
			return fmt.Errorf("ts_regex_subs[%d]: pattern is required", i)
		}
		if _, err := regexp.Compile(s.Pattern); err != nil {
			return fmt.Errorf("ts_regex_subs[%d]: %w", i, err)
		}
	}
	return nil
}

// Fields returns the named capture groups in the order they appear in
// the regex. Drives the preview table's column order — map iteration
// order would shuffle the columns on every render, which makes a live
// preview unreadable.
func (d *Draft) Fields() []string {
	re, err := regexp.Compile(d.Parse)
	if err != nil {
		return nil
	}
	var out []string
	for i, n := range re.SubexpNames() {
		if i == 0 || n == "" {
			continue
		}
		out = append(out, n)
	}
	return out
}

// rawRule converts the draft into the loosely-typed form the ingest
// layer's adapters compile, so the preview runs through exactly the
// same code path as a rule loaded from disk. Anything else — a
// reimplementation, a shortcut — would let the preview and the result
// disagree, which is the one failure a preview cannot have.
func (d *Draft) rawRule() ingest.RawRule {
	data := map[string]any{
		"family":    "line",
		"name":      d.Name,
		"priority":  d.priority(),
		"parse":     d.Parse,
		"ts_layout": d.TsLayout,
	}
	if d.TsAssumeTZ != "" {
		data["ts_assume_tz"] = d.TsAssumeTZ
	}
	if d.TsUseMtimeDate {
		data["ts_use_mtime_date"] = true
	}
	if d.TsUseMtimeYear {
		data["ts_use_mtime_year"] = true
	}
	if d.MessageField != "" {
		data["message_field"] = d.MessageField
	}
	if len(d.TsRegexSubs) > 0 {
		subs := make([]any, 0, len(d.TsRegexSubs))
		for _, s := range d.TsRegexSubs {
			subs = append(subs, map[string]any{
				"pattern":     s.Pattern,
				"replacement": s.Replacement,
			})
		}
		data["ts_regex_subs"] = subs
	}
	return ingest.RawRule{
		Source: "draft:" + d.Name,
		Family: "line",
		Name:   d.Name,
		Data:   data,
	}
}

func (d *Draft) priority() int {
	if d.Priority > 0 {
		return d.Priority
	}
	return defaultPriority
}

// YAML renders the draft as a rule file.
//
// Hand-rolled rather than yaml.Marshal because the sample lines belong
// in the file as a header comment and a marshaller drops comments. The
// output is meant to be opened, read and edited by hand afterwards —
// matching the shape of the shipped rules is the point, not incidental.
func (d *Draft) YAML() []byte {
	var b strings.Builder

	b.WriteString("# " + d.Name + "\n")
	b.WriteString("#\n")
	if s := strings.TrimSpace(d.Sample); s != "" {
		b.WriteString("# Sample lines this rule was built from:\n")
		b.WriteString("#\n")
		for i, ln := range strings.Split(s, "\n") {
			if i >= sampleCommentLines {
				b.WriteString("#   …\n")
				break
			}
			b.WriteString("#   " + strings.TrimRight(ln, " \t\r") + "\n")
		}
		b.WriteString("#\n")
	}
	b.WriteString("# Written by the rule builder. Edit freely — this is an ordinary\n")
	b.WriteString("# rule file, and nothing reads it back into the builder.\n\n")

	b.WriteString("family: line\n")
	b.WriteString("name: " + quoteYAML(d.Name) + "\n")
	fmt.Fprintf(&b, "priority: %d\n\n", d.priority())

	b.WriteString("parse: " + quoteYAML(d.Parse) + "\n\n")
	b.WriteString("ts_layout: " + quoteYAML(d.TsLayout) + "\n")
	if d.TsAssumeTZ != "" {
		b.WriteString("ts_assume_tz: " + quoteYAML(d.TsAssumeTZ) + "\n")
	}
	if d.TsUseMtimeDate {
		b.WriteString("ts_use_mtime_date: true\n")
	}
	if d.TsUseMtimeYear {
		b.WriteString("ts_use_mtime_year: true\n")
	}
	if d.MessageField != "" {
		b.WriteString("message_field: " + quoteYAML(d.MessageField) + "\n")
	}
	if len(d.TsRegexSubs) > 0 {
		b.WriteString("\nts_regex_subs:\n")
		for _, s := range d.TsRegexSubs {
			b.WriteString("  - pattern: " + quoteYAML(s.Pattern) + "\n")
			b.WriteString("    replacement: " + quoteYAML(s.Replacement) + "\n")
		}
	}
	return []byte(b.String())
}

// sampleCommentLines caps how much of the sample is echoed into the
// rule file's header. Enough to recognize the format at a glance,
// not enough to turn a rule file into a log file.
const sampleCommentLines = 6

// quoteYAML renders s as a single-quoted YAML scalar.
//
// Single quotes are the only YAML string form with no escape sequences
// at all — the sole rule is that an embedded quote is doubled. That is
// exactly what a regex needs: `\d{2}` and `\\` survive byte for byte,
// where a double-quoted scalar would eat the backslashes.
func quoteYAML(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

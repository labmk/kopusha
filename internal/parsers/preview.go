package parsers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/labmk/obs-viewer/internal/ingest"
	"github.com/labmk/obs-viewer/internal/ingest/line"
)

// PreviewLine is one sample line and what the rule did with it.
type PreviewLine struct {
	Text string `json:"text"`
	// Status is "parsed" when the regex matched and opened a record, or
	// "continuation" when it did not and the line was appended to the
	// previous record's message field.
	Status string `json:"status"`
	// TS is the raw text the ts group captured, before any layout is
	// applied. Present only on parsed lines.
	TS string `json:"ts,omitempty"`
	// TSError is why that text could not be turned into a timestamp.
	TSError string `json:"ts_error,omitempty"`
}

// Preview is what a candidate rule would produce from a sample.
type Preview struct {
	// Fields are the capture-group names in regex order, which is the
	// column order the preview table uses.
	Fields []string         `json:"fields"`
	Rows   []map[string]any `json:"rows"`
	Lines  []PreviewLine    `json:"lines"`
	// Parsed and Continuation count the sample lines by status. A rule
	// that parses one line in twenty is matching by accident.
	Parsed       int `json:"parsed"`
	Continuation int `json:"continuation"`
	// TimestampErrors counts parsed lines whose captured timestamp the
	// layout rejected.
	//
	// This is the failure worth shouting about. The adapter drops a
	// timestamp it cannot parse and keeps the record, so the rule looks
	// like it works — rows appear, fields are populated — and every one
	// of them is undated, which the operator discovers later when a time
	// filter returns nothing.
	TimestampErrors int      `json:"timestamp_errors"`
	Warnings        []string `json:"warnings,omitempty"`
	Error           string   `json:"error,omitempty"`
}

// previewSampleLines caps how many sample lines are run through the
// adapter. The preview is re-run as the operator types.
const previewSampleLines = 200

// PreviewDraft runs a candidate rule over sample text and reports what
// it produced.
//
// The sample is written to a temp file and handed to the real line
// adapter rather than being matched here directly. That costs a file
// write per keystroke-debounce and buys the only property that matters:
// the preview cannot disagree with the result. Continuation-line
// handling, BOM skipping, the mtime-date rollover, oversized-line
// truncation — all of it is adapter behavior that a second
// implementation would get subtly wrong, and a preview that lies is
// worse than no preview.
func PreviewDraft(ctx context.Context, d Draft, sample string) Preview {
	var p Preview

	if err := d.Validate(); err != nil {
		p.Error = err.Error()
		return p
	}
	lines := sampleLines(sample, previewSampleLines)
	if len(lines) == 0 {
		p.Error = "paste some sample lines to see what this rule would produce"
		return p
	}

	// The adapter needs a name to build a rule; the operator has not
	// necessarily supplied one yet.
	if strings.TrimSpace(d.Name) == "" {
		d.Name = "preview"
	}
	p.Fields = d.Fields()
	p.Warnings = d.Warnings

	loader, err := line.New([]ingest.RawRule{d.rawRule()})
	if err != nil {
		p.Error = err.Error()
		return p
	}

	tmp, err := writeSampleFile(lines)
	if err != nil {
		p.Error = fmt.Sprintf("could not stage the sample: %v", err)
		return p
	}
	defer os.Remove(tmp)

	st, err := os.Stat(tmp)
	if err != nil {
		p.Error = fmt.Sprintf("could not stage the sample: %v", err)
		return p
	}
	hint := ingest.LoadHint{
		Path:  tmp,
		Ext:   ".log",
		MTime: st.ModTime(),
	}

	err = loader.Stream(ctx, hint, func(r ingest.Record) error {
		row := make(map[string]any, len(r))
		for k, v := range r {
			row[k] = v
		}
		p.Rows = append(p.Rows, row)
		return nil
	})
	if err != nil {
		p.Error = err.Error()
		return p
	}

	p.Lines = classify(lines, d)
	for _, l := range p.Lines {
		switch {
		case l.Status == "parsed":
			p.Parsed++
			if l.TSError != "" {
				p.TimestampErrors++
			}
		default:
			p.Continuation++
		}
	}
	return p
}

// classify re-runs the rule's regex over each sample line to report,
// per line, whether it opened a record or was folded into the previous
// one — and, for the lines that matched, whether the captured timestamp
// survived the layout.
//
// The adapter cannot answer either question: it emits records, not line
// verdicts, and it discards a timestamp parse error silently by design
// (one malformed line should not fail a 2 GB ingest). So the per-line
// view is reconstructed here from the same regex the adapter compiled.
// The reconstruction is honest because it is only ever used to explain
// the rows, never to produce them.
func classify(lines []string, d Draft) []PreviewLine {
	re, err := regexp.Compile(d.Parse)
	if err != nil {
		return nil
	}
	tsIdx := re.SubexpIndex("ts")

	loc := time.UTC
	if d.TsAssumeTZ != "" {
		if l, err := time.LoadLocation(d.TsAssumeTZ); err == nil {
			loc = l
		}
	}
	subs := make([]*regexp.Regexp, len(d.TsRegexSubs))
	for i, s := range d.TsRegexSubs {
		subs[i], _ = regexp.Compile(s.Pattern)
	}

	out := make([]PreviewLine, 0, len(lines))
	for _, ln := range lines {
		m := re.FindStringSubmatch(ln)
		if m == nil {
			out = append(out, PreviewLine{Text: ln, Status: "continuation"})
			continue
		}
		pl := PreviewLine{Text: ln, Status: "parsed"}
		if tsIdx > 0 && tsIdx < len(m) {
			raw := m[tsIdx]
			pl.TS = raw
			for i, s := range subs {
				if s != nil {
					raw = s.ReplaceAllString(raw, d.TsRegexSubs[i].Replacement)
				}
			}
			if _, err := time.ParseInLocation(d.TsLayout, raw, loc); err != nil {
				pl.TSError = tsErrorText(raw, d.TsLayout, err)
			}
		}
		out = append(out, pl)
	}
	return out
}

// tsErrorText rewrites Go's time-parse error into something aimed at
// the person editing the layout. The stock message quotes the layout
// and the input back with an "as" between them, which reads as noise
// until you already know how Go layouts work.
func tsErrorText(raw, layout string, err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ": "); i >= 0 {
		msg = msg[i+2:]
	}
	return fmt.Sprintf("%q does not match the layout %q (%s)", raw, layout, msg)
}

// writeSampleFile stages the sample where the adapter can open it.
func writeSampleFile(lines []string) (string, error) {
	f, err := os.CreateTemp("", "obs-viewer-rule-preview-*.log")
	if err != nil {
		return "", err
	}
	name := f.Name()
	_, werr := f.WriteString(strings.Join(lines, "\n") + "\n")
	cerr := f.Close()
	if werr != nil || cerr != nil {
		os.Remove(name)
		if werr != nil {
			return "", werr
		}
		return "", cerr
	}
	return filepath.Clean(name), nil
}

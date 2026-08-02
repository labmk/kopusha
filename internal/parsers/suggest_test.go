package parsers

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// The suggestion is only as good as what it does to real lines, so the
// table is samples in and parsed rows out. Each case asserts the whole
// chain — infer a rule, run it through the actual line adapter, and
// require that every sample line produced a record with a timestamp the
// layout accepted.
//
// Undated rows are the failure mode worth guarding: a rule whose regex
// matches but whose layout does not produces rows that look right and
// filter to nothing.
func TestSuggestParsesItsOwnSample(t *testing.T) {
	cases := []struct {
		name       string
		sample     string
		wantFields []string // capture names that must be present
		wantLayout string
	}{
		{
			name: "iso with bracketed metadata",
			sample: `2026-03-25 00:31:11.231 [INF] [CS] [PID:4252] clientManagement.xml.enc
2026-03-25 00:31:11.402 [WRN] [DB] [PID:4252] retrying connection
2026-03-25 00:31:12.007 [ERR] [CS] [PID:8817] handshake failed`,
			wantFields: []string{"ts", "level", "pid", "message"},
			wantLayout: "2006-01-02 15:04:05",
		},
		{
			name: "rfc3339 with zulu zone",
			sample: `2026-03-18T06:00:00.123Z INFO  queue depth 2347
2026-03-18T06:00:01.900Z WARN  queue depth 4102
2026-03-18T06:00:03.001Z ERROR queue stalled`,
			wantFields: []string{"ts", "level", "message"},
			wantLayout: "2006-01-02T15:04:05Z07:00",
		},
		{
			name: "rfc3339 with numeric offset",
			sample: `2026-03-18T06:00:00+0200 INFO  started
2026-03-18T06:00:01+0200 DEBUG loaded config`,
			wantFields: []string{"ts", "level", "message"},
			wantLayout: "2006-01-02T15:04:05Z0700",
		},
		{
			name: "day-first dashed date",
			sample: `28-02-2026 04:00:40.417 Info    Analyzing Configuration
28-02-2026 04:00:41.002 Warn    Configuration incomplete
28-02-2026 04:00:41.881 Error   Giving up`,
			wantFields: []string{"ts", "level", "message"},
			wantLayout: "02-01-2006 15:04:05",
		},
		{
			name: "syslog, no year in the line",
			sample: `Mar 18 06:00:00 gw sshd: accepted publickey
Mar 18 06:00:04 gw sshd: session opened
Mar  8 06:00:09 gw cron: running job`,
			wantFields: []string{"ts", "message"},
			wantLayout: "Jan _2 15:04:05",
		},
		{
			name: "combined log format",
			sample: `10.0.0.1 - - [18/Mar/2026:06:00:00 +0000] "GET /a HTTP/1.1" 200 512
10.0.0.7 - - [18/Mar/2026:06:00:01 +0000] "GET /b HTTP/1.1" 404 91`,
			wantFields: []string{"ts", "message"},
			wantLayout: "02/Jan/2006:15:04:05 -0700",
		},
		{
			name: "time of day only",
			sample: `15:13:52.592268 26804/46320 Info   CaseViewActivationSetter created
15:13:53.104881 26804/46320 Debug  Cache warmed
15:13:54.900012 26804/11902 Error  Activation failed`,
			wantFields: []string{"ts", "level", "message"},
			wantLayout: "15:04:05",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Suggest(tc.sample)
			if err := d.Validate(); err != nil {
				t.Fatalf("suggested draft does not validate: %v\nparse: %s", err, d.Parse)
			}
			if d.TsLayout != tc.wantLayout {
				t.Errorf("ts_layout = %q, want %q", d.TsLayout, tc.wantLayout)
			}

			got := map[string]bool{}
			for _, f := range d.Fields() {
				got[f] = true
			}
			for _, want := range tc.wantFields {
				if !got[want] {
					t.Errorf("missing capture %q; got %v\nparse: %s", want, d.Fields(), d.Parse)
				}
			}

			p := PreviewDraft(context.Background(), d, tc.sample)
			if p.Error != "" {
				t.Fatalf("preview failed: %s\nparse: %s", p.Error, d.Parse)
			}
			wantLines := len(sampleLines(tc.sample, maxSampleLines))
			if p.Parsed != wantLines {
				t.Errorf("parsed %d of %d lines (%d treated as continuations)\nparse: %s",
					p.Parsed, wantLines, p.Continuation, d.Parse)
				for _, l := range p.Lines {
					if l.Status != "parsed" {
						t.Logf("  unmatched: %s", l.Text)
					}
				}
			}
			if p.TimestampErrors != 0 {
				for _, l := range p.Lines {
					if l.TSError != "" {
						t.Errorf("timestamp not parsed: %s", l.TSError)
					}
				}
			}
			if len(p.Rows) != wantLines {
				t.Errorf("got %d rows, want %d", len(p.Rows), wantLines)
			}
		})
	}
}

// A capture that follows a label should carry the label's name, because
// field1/field2 make the operator do the renaming that the sample
// already answered.
func TestSuggestNamesLabelledFields(t *testing.T) {
	d := Suggest(`2026-03-25 00:31:11.231 [PID:4252] started
2026-03-25 00:31:12.100 [PID:8817] started`)
	found := false
	for _, f := range d.Fields() {
		if f == "pid" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a capture named pid, got %v\nparse: %s", d.Fields(), d.Parse)
	}
}

// A sample where every line shares a level must still capture it. The
// level being constant is a fact about the sample, not the format.
func TestSuggestNeverFreezesTheLevel(t *testing.T) {
	d := Suggest(`2026-03-25 00:31:11.231 INFO first
2026-03-25 00:31:12.100 INFO second`)
	if !strings.Contains(d.Parse, "(?P<level>") {
		t.Errorf("level was baked in as a literal: %s", d.Parse)
	}
	p := PreviewDraft(context.Background(), d, `2026-03-25 00:31:13.400 ERROR third`)
	if p.Parsed != 1 {
		t.Errorf("a line with a different level did not parse: %s", d.Parse)
	}
}

// Column-aligned formats pad the level to a fixed width, so the run of
// spaces after it changes with the value.
func TestSuggestToleratesVariableSpacing(t *testing.T) {
	d := Suggest(`2026-03-25 00:31:11.231 INFO    short level
2026-03-25 00:31:12.100 WARNING wide level`)
	p := PreviewDraft(context.Background(), d, `2026-03-25 00:31:13.400 ERROR   another`)
	if p.Parsed != 1 {
		t.Errorf("padded level did not parse: %s", d.Parse)
	}
}

// 03-04-2026 is ambiguous and no amount of sample will settle it. The
// requirement is that it says so.
func TestSuggestWarnsOnAmbiguousDateOrder(t *testing.T) {
	d := Suggest(`03-04-2026 04:00:40.417 Info one
05-04-2026 04:00:41.002 Info two`)
	if len(d.Warnings) == 0 {
		t.Fatal("expected a warning about day/month order")
	}
	found := false
	for _, w := range d.Warnings {
		if strings.Contains(w, "order") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings do not mention the ordering: %v", d.Warnings)
	}
}

// A day above 12 settles it, and then there is nothing to warn about.
func TestSuggestResolvesDateOrderFromEvidence(t *testing.T) {
	d := Suggest(`28-02-2026 04:00:40.417 Info one
03-04-2026 04:00:41.002 Info two`)
	if d.TsLayout != "02-01-2006 15:04:05" {
		t.Errorf("ts_layout = %q, want day-first", d.TsLayout)
	}
	for _, w := range d.Warnings {
		if strings.Contains(w, "guess") {
			t.Errorf("warned about an order the sample settles: %v", d.Warnings)
		}
	}
}

// A prefix that varies in shape before the timestamp cannot be aligned
// token by token, but the rule still has to work.
func TestSuggestHandlesVariablePrefix(t *testing.T) {
	sample := `app.Common.CacheService: 26.03.26 09:55:01:280 36800/113264 creation started
svc.Api: 26.03.26 09:55:02:100 36800/113264 request handled
a.b.c.d.Deep.Name: 26.03.26 09:55:03:900 36800/98 done`
	d := Suggest(sample)
	if err := d.Validate(); err != nil {
		t.Fatalf("draft does not validate: %v", err)
	}
	p := PreviewDraft(context.Background(), d, sample)
	if p.Error != "" {
		t.Fatalf("preview failed: %s", p.Error)
	}
	if p.Parsed != 3 {
		t.Errorf("parsed %d of 3\nparse: %s", p.Parsed, d.Parse)
	}
	// HH:MM:SS:mmm is not expressible as a Go layout, so the suggestion
	// has to have produced a substitution for it.
	if len(d.TsRegexSubs) == 0 {
		t.Errorf("expected a ts_regex_subs entry for the colon-separated milliseconds")
	}
	if p.TimestampErrors != 0 {
		t.Errorf("%d timestamps failed to parse", p.TimestampErrors)
	}
}

// Nothing recognizable must produce advice rather than a broken rule.
func TestSuggestWithNoTimestamp(t *testing.T) {
	d := Suggest("this line has no timestamp at all\nnor does this one")
	if d.Parse != "" {
		t.Errorf("invented a rule for a sample with no timestamp: %s", d.Parse)
	}
	if len(d.Warnings) == 0 {
		t.Error("expected a warning")
	}
}

// Every generated regex has to compile and name its groups uniquely —
// Go's regexp rejects duplicates outright.
func TestSuggestProducesUniqueGroupNames(t *testing.T) {
	d := Suggest(`2026-03-25 00:31:11.231 [a:1] [b:2] [c:3] msg
2026-03-25 00:31:12.100 [x:9] [y:8] [z:7] msg`)
	re, err := regexp.Compile(d.Parse)
	if err != nil {
		t.Fatalf("generated regex does not compile: %v\n%s", err, d.Parse)
	}
	seen := map[string]bool{}
	for _, n := range re.SubexpNames() {
		if n == "" {
			continue
		}
		if seen[n] {
			t.Errorf("duplicate group name %q in %s", n, d.Parse)
		}
		seen[n] = true
	}
}

// CRLF is invisible in an editor and fatal to a regex anchored with $.
func TestSuggestFoldsCRLF(t *testing.T) {
	sample := "2026-03-25 00:31:11.231 INFO one\r\n2026-03-25 00:31:12.100 WARN two\r\n"
	d := Suggest(sample)
	p := PreviewDraft(context.Background(), d, sample)
	if p.Parsed != 2 {
		t.Errorf("parsed %d of 2 CRLF lines\nparse: %s", p.Parsed, d.Parse)
	}
	for _, r := range p.Rows {
		if msg, ok := r["message"].(string); ok && strings.HasSuffix(msg, "\r") {
			t.Errorf("carriage return survived into the message: %q", msg)
		}
	}
}

// The preview has to name a timestamp failure rather than showing rows
// that quietly have no time on them.
func TestPreviewReportsTimestampFailure(t *testing.T) {
	d := Draft{
		Name:     "wrong-layout",
		Parse:    `^(?P<ts>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) (?P<message>.*)$`,
		TsLayout: "02/01/2006 15:04:05",
	}
	p := PreviewDraft(context.Background(), d, "2026-03-25 00:31:11 hello")
	if p.Parsed != 1 {
		t.Fatalf("expected the line to match the regex, got %d parsed", p.Parsed)
	}
	if p.TimestampErrors != 1 {
		t.Errorf("expected 1 timestamp error, got %d", p.TimestampErrors)
	}
	if !strings.Contains(p.Lines[0].TSError, "does not match the layout") {
		t.Errorf("unhelpful error text: %q", p.Lines[0].TSError)
	}
}

// A strftime pattern is the first thing anyone types into a field
// labelled "timestamp format".
func TestValidateRejectsStrftime(t *testing.T) {
	d := Draft{
		Name:     "x",
		Parse:    `^(?P<ts>\S+) (?P<message>.*)$`,
		TsLayout: "%Y-%m-%d %H:%M:%S",
	}
	err := d.Validate()
	if err == nil || !strings.Contains(err.Error(), "strftime") {
		t.Errorf("expected a strftime-specific message, got %v", err)
	}
}

func TestValidateRequiresTsGroup(t *testing.T) {
	d := Draft{
		Name:     "x",
		Parse:    `^(?P<when>\S+) (?P<message>.*)$`,
		TsLayout: "2006-01-02",
	}
	err := d.Validate()
	if err == nil || !strings.Contains(err.Error(), "(?P<ts>") {
		t.Errorf("expected a message naming the ts group, got %v", err)
	}
}

// Lines that do not match are folded into the previous record. The
// preview has to show that, because a rule that silently swallows half
// the file into message fields looks like a rule that works.
func TestPreviewMarksContinuationLines(t *testing.T) {
	sample := `2026-03-25 00:31:11.231 INFO request failed
    at Handler.process(Handler.java:42)
    at Server.dispatch(Server.java:19)
2026-03-25 00:31:12.100 INFO recovered`
	d := Suggest(sample)
	p := PreviewDraft(context.Background(), d, sample)
	if p.Parsed != 2 {
		t.Errorf("parsed = %d, want 2\nparse: %s", p.Parsed, d.Parse)
	}
	if p.Continuation != 2 {
		t.Errorf("continuation = %d, want 2", p.Continuation)
	}
	if len(p.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(p.Rows))
	}
	msg, _ := p.Rows[0]["message"].(string)
	if !strings.Contains(msg, "Handler.java:42") {
		t.Errorf("the stack frame did not land in the first record's message: %q", msg)
	}
}

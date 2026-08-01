// Package line is the Loader for single-line text logs.
//
// A "rule" describes one line format via a single regex with named
// capture groups. The capture named "ts" is required and becomes
// @timestamp; every other named group becomes a record field.
//
// Lines that don't match the rule's parse regex are treated as
// continuations of the previous record and appended to its message
// field. This is how multi-line exception traces, indented blocks, and
// underlines (===== Header =====) stay attached to the right log entry.
//
// When a format carries only a time-of-day with no date (the *.out
// case), the rule sets ts_use_mtime_date: true. The file mtime supplies
// the working date; the adapter advances by one calendar day whenever
// the parsed time-of-day jumps backwards more than 12 hours, so a file
// that spans a single midnight rollover still gets monotonic
// timestamps.
//
// Rule format and the regex itself live in parsers.d/*.yaml — no
// product or filename strings appear in this package's code.
package line

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/labmk/obs-viewer/internal/ingest"
)

const utf8BOM = "\xef\xbb\xbf"

// sub is one (pattern, replacement) pair applied to a captured ts
// string before time.Parse — needed for formats where the time
// separator deviates from anything Go's layout language can express
// (e.g. HH:MM:SS:ms with a colon instead of a dot before ms).
type sub struct {
	Pattern *regexp.Regexp
	Replace string
}

// Rule is one compiled line-format rule loaded from YAML.
type Rule struct {
	Name           string
	Priority       int            // confidence score returned by Detect when matched
	Parse          *regexp.Regexp // must define (?P<ts>...)
	TsLayout       string         // Go time layout for the captured ts
	TsAssumeTZ     *time.Location // location applied to naive ts parses; nil = UTC
	TsUseMtimeDate bool           // ts captures only time-of-day; combine with mtime date
	TsRegexSubs    []sub          // applied to captured ts before time.Parse
	MessageField   string         // field name for continuation appends; default "message"
}

// Loader is the line-log adapter.
type Loader struct {
	rules []Rule
}

// New compiles raw YAML rules. An empty rule set is legal — the loader
// will refuse every file (Detect returns 0).
func New(raw []ingest.RawRule) (*Loader, error) {
	out := make([]Rule, 0, len(raw))
	for _, r := range raw {
		rule, err := compileRule(r)
		if err != nil {
			return nil, fmt.Errorf("line rule from %s: %w", r.Source, err)
		}
		out = append(out, rule)
	}
	return &Loader{rules: out}, nil
}

// Name reports the loader name.
func (Loader) Name() string { return "line" }

// detectSniffBytes is how much of the file the line adapter is willing
// to scan when looking for a matching rule. Larger than the dispatcher's
// 512-byte sniff so banner/header text before the first properly
// formatted log line doesn't fool autodetect.
const detectSniffBytes = 16 * 1024

// Detect returns the priority of the best-matching rule. A rule is
// considered to match when at least one non-blank line of the head of
// the file (up to detectSniffBytes) parses successfully — that covers
// files whose first real log entry is preceded by a config dump or
// banner.
func (l *Loader) Detect(h ingest.LoadHint) int {
	lines := sniffLines(h, detectSniffBytes)
	best := 0
	for _, r := range l.rules {
		if anyLineMatches(r.Parse, lines, 200) && r.Priority > best {
			best = r.Priority
		}
	}
	return best
}

// Stream emits one Record per parsed line. Non-matching lines are
// appended to the previous record's MessageField. If no record is
// open yet (the file starts with non-matching banner text), continuation
// lines are silently dropped.
func (l *Loader) Stream(ctx context.Context, h ingest.LoadHint, emit func(ingest.Record) error) error {
	rule, ok := l.pickRule(h)
	if !ok {
		return fmt.Errorf("line: no rule matched %s", h.Path)
	}

	f, err := os.Open(h.Path)
	if err != nil {
		return fmt.Errorf("line: open: %w", err)
	}
	defer f.Close()
	if err := skipBOM(f); err != nil {
		return fmt.Errorf("line: skip BOM: %w", err)
	}

	// bufio.Reader instead of bufio.Scanner so a single overlong line
	// (binary log payload, base64 blob spanning megabytes) doesn't
	// terminate the whole ingest. ReadLineBounded truncates the line
	// and resumes from the next newline.
	br := bufio.NewReaderSize(f, 64*1024)

	names := rule.Parse.SubexpNames()
	tsIdx := rule.Parse.SubexpIndex("ts")
	msgField := rule.MessageField
	if msgField == "" {
		msgField = "message"
	}

	loc := time.UTC
	if rule.TsAssumeTZ != nil {
		loc = rule.TsAssumeTZ
	}
	mtimeLocal := h.MTime.In(loc)
	workingDate := time.Date(mtimeLocal.Year(), mtimeLocal.Month(), mtimeLocal.Day(), 0, 0, 0, 0, loc)
	var lastTOD time.Duration
	haveTOD := false

	var rec ingest.Record
	flush := func() error {
		if rec == nil {
			return nil
		}
		out := rec
		rec = nil
		return emit(out)
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, truncated, err := ingest.ReadLineBounded(br, ingest.MaxLineBytes)
		if err != nil && err != io.EOF {
			return fmt.Errorf("line: read: %w", err)
		}
		eof := err == io.EOF
		if line == "" && eof {
			break
		}
		// Drop truncated lines wholesale: a 16+ MiB line is almost
		// certainly a binary payload or corruption, and treating it
		// as a continuation would poison the previous record's
		// message field with megabytes of junk.
		if truncated {
			if eof {
				break
			}
			continue
		}
		m := rule.Parse.FindStringSubmatch(line)
		if m == nil {
			if rec != nil && strings.TrimSpace(line) != "" {
				if existing, ok := rec[msgField].(string); ok {
					rec[msgField] = existing + "\n" + line
				} else {
					rec[msgField] = line
				}
			}
			continue
		}
		if err := flush(); err != nil {
			return err
		}
		rec = ingest.Record{ingest.FieldSource: "line:" + rule.Name}
		for i, n := range names {
			if i == 0 || n == "" {
				continue
			}
			rec[n] = m[i]
		}
		if tsIdx > 0 {
			raw := m[tsIdx]
			for _, s := range rule.TsRegexSubs {
				raw = s.Pattern.ReplaceAllString(raw, s.Replace)
			}
			if rule.TsUseMtimeDate {
				if t, err := time.ParseInLocation(rule.TsLayout, raw, loc); err == nil {
					tod := time.Duration(t.Hour())*time.Hour +
						time.Duration(t.Minute())*time.Minute +
						time.Duration(t.Second())*time.Second +
						time.Duration(t.Nanosecond())
					if haveTOD && tod+12*time.Hour < lastTOD {
						workingDate = workingDate.AddDate(0, 0, 1)
					}
					full := workingDate.Add(tod)
					rec[ingest.FieldTimestamp] = full.UTC().Format(time.RFC3339Nano)
					lastTOD = tod
					haveTOD = true
				}
			} else {
				if t, err := time.ParseInLocation(rule.TsLayout, raw, loc); err == nil {
					rec[ingest.FieldTimestamp] = t.UTC().Format(time.RFC3339Nano)
				}
			}
		}
		if eof {
			break
		}
	}
	return flush()
}

func (l *Loader) pickRule(h ingest.LoadHint) (Rule, bool) {
	lines := sniffLines(h, detectSniffBytes)
	var best Rule
	bestPriority := -1
	for _, r := range l.rules {
		if anyLineMatches(r.Parse, lines, 200) && r.Priority > bestPriority {
			best = r
			bestPriority = r.Priority
		}
	}
	return best, bestPriority >= 0
}

// sniffLines returns up to maxBytes from the head of the file as a
// slice of lines (BOM stripped, CRLF folded). Uses h.Sniff when it is
// already at least maxBytes; otherwise (or when it's empty) opens the
// file and reads maxBytes itself. Lets the line adapter look past
// banner/header text that fits in the dispatcher's smaller sniff but
// hides the first format-bearing line.
func sniffLines(h ingest.LoadHint, maxBytes int) [][]byte {
	buf := h.Sniff
	if len(buf) < maxBytes && h.Path != "" {
		if f, err := os.Open(h.Path); err == nil {
			bigger := make([]byte, maxBytes)
			n, _ := io.ReadFull(f, bigger)
			buf = bigger[:n]
			f.Close()
		}
	}
	return splitLines(normalizeSniff(buf))
}

func splitLines(b []byte) [][]byte {
	return bytes.Split(b, []byte("\n"))
}

func anyLineMatches(re *regexp.Regexp, lines [][]byte, limit int) bool {
	checked := 0
	for _, line := range lines {
		if checked >= limit {
			break
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		checked++
		if re.Match(line) {
			return true
		}
	}
	return false
}

func normalizeSniff(b []byte) []byte {
	b = bytes.TrimPrefix(b, []byte(utf8BOM))
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

func skipBOM(f *os.File) error {
	buf := make([]byte, 3)
	n, err := io.ReadFull(f, buf)
	if err == io.EOF {
		return nil
	}
	if err != nil && err != io.ErrUnexpectedEOF {
		return err
	}
	if n == 3 && string(buf) == utf8BOM {
		return nil
	}
	_, err = f.Seek(0, io.SeekStart)
	return err
}

func compileRule(r ingest.RawRule) (Rule, error) {
	name := strOr(r.Data, "name", r.Name, "unnamed")
	parse := strOr(r.Data, "parse", "")
	if parse == "" {
		return Rule{}, fmt.Errorf("rule %q: parse is required", name)
	}
	layout := strOr(r.Data, "ts_layout", "")
	if layout == "" {
		return Rule{}, fmt.Errorf("rule %q: ts_layout is required", name)
	}

	parseRe, err := regexp.Compile(parse)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %q: parse regex: %w", name, err)
	}
	if parseRe.SubexpIndex("ts") < 0 {
		return Rule{}, fmt.Errorf("rule %q: parse regex must define (?P<ts>...)", name)
	}

	var loc *time.Location
	if tz, ok := r.Data["ts_assume_tz"].(string); ok && tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return Rule{}, fmt.Errorf("rule %q: ts_assume_tz: %w", name, err)
		}
		loc = l
	}

	subs, err := compileSubs(r.Data["ts_regex_subs"], name)
	if err != nil {
		return Rule{}, err
	}

	return Rule{
		Name:           name,
		Priority:       intOr(r.Data, "priority", 10),
		Parse:          parseRe,
		TsLayout:       layout,
		TsAssumeTZ:     loc,
		TsUseMtimeDate: boolOr(r.Data, "ts_use_mtime_date", false),
		TsRegexSubs:    subs,
		MessageField:   strOr(r.Data, "message_field", "", "message"),
	}, nil
}

func compileSubs(raw any, ruleName string) ([]sub, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("rule %q: ts_regex_subs must be a list", ruleName)
	}
	out := make([]sub, 0, len(list))
	for i, x := range list {
		m, ok := x.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("rule %q: ts_regex_subs[%d]: not a mapping", ruleName, i)
		}
		pat, _ := m["pattern"].(string)
		rep, _ := m["replacement"].(string)
		if pat == "" {
			return nil, fmt.Errorf("rule %q: ts_regex_subs[%d]: pattern required", ruleName, i)
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("rule %q: ts_regex_subs[%d]: %w", ruleName, i, err)
		}
		out = append(out, sub{Pattern: re, Replace: rep})
	}
	return out, nil
}

func strOr(m map[string]any, key string, fallbacks ...string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	for _, f := range fallbacks {
		if f != "" {
			return f
		}
	}
	return ""
}

func intOr(m map[string]any, key string, def int) int {
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return def
}

func boolOr(m map[string]any, key string, def bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

// Package block is the Loader for separator-delimited key:value records.
//
// Format shape (one rule covers all observed samples):
//
//	----------------------------------------
//	Key: Value
//	OtherKey: Other value
//	  continuation of OtherKey's value
//	-- can continue across lines that don't look like a new key --
//	----------------------------------------
//	Key: ...
//
// Records are delimited by a line that matches the rule's separator
// regex (default: 20+ dashes). Inside a record, lines that match the
// field regex `^(?P<key>…):[ \t]?(?P<value>.*)$` set a key. Lines that
// don't match — but aren't blank — are appended to the previous key's
// value with a newline separator. This is how multi-line Messages are
// reconstructed without losing the original formatting.
//
// The adapter is product-agnostic: rules live in YAML files under
// parsers.d/ and the user can ship additional rule files (different
// separator, different field syntax) without touching this code.
package block

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/labmk/kopusha/internal/ingest"
)

// utf8BOM is the byte-order mark Windows tooling writes at the start of
// many UTF-8 text files. We strip it once before regex sniffing and
// before line scanning so rules don't need to know it exists.
const utf8BOM = "\xef\xbb\xbf"

// Rule is one compiled block-format rule loaded from YAML.
type Rule struct {
	Name      string
	Priority  int            // confidence score returned by Detect when matched
	Sniff     *regexp.Regexp // multi-line; matched against LoadHint.Sniff
	Separator *regexp.Regexp // matched against each line of the file
	Field     *regexp.Regexp // must have named groups "key" and "value"
	TsField   []string       // candidate field names to resolve @timestamp
	TsLayouts []string       // Go time layouts tried in order against TsField
}

// Loader is the block-record adapter.
type Loader struct {
	rules []Rule
}

// New compiles raw YAML rules. An empty rule set is legal — the loader
// will just refuse every file (Detect returns 0).
func New(raw []ingest.RawRule) (*Loader, error) {
	out := make([]Rule, 0, len(raw))
	for _, r := range raw {
		c, err := compileRule(r)
		if err != nil {
			return nil, fmt.Errorf("block rule from %s: %w", r.Source, err)
		}
		out = append(out, c)
	}
	return &Loader{rules: out}, nil
}

// Name reports the loader name.
func (Loader) Name() string { return "block" }

// Detect returns the priority of the first rule whose sniff regex
// matches the sniff buffer. Highest priority wins when multiple rules
// match (handled by the dispatcher's tie-break, not here).
func (l *Loader) Detect(h ingest.LoadHint) int {
	sniff := normalizeSniff(h.Sniff)
	best := 0
	for _, r := range l.rules {
		if r.Sniff.Match(sniff) && r.Priority > best {
			best = r.Priority
		}
	}
	return best
}

// Explain names the rule that matched, or reports how many were tried.
// The rule names are the actionable part: they map one-to-one onto
// files in parsers.d/, so "none of these matched" tells the operator
// exactly which files to look at — or that a new one is needed.
func (l *Loader) Explain(h ingest.LoadHint) string {
	if len(l.rules) == 0 {
		return "no block rules loaded"
	}
	sniff := normalizeSniff(h.Sniff)
	var matched []string
	for _, r := range l.rules {
		if r.Sniff.Match(sniff) {
			matched = append(matched, r.Name)
		}
	}
	if len(matched) > 0 {
		return "sniff matched rule " + quoteList(matched)
	}
	return fmt.Sprintf("no separator line found in the first %d bytes; tried %d rule(s): %s",
		len(h.Sniff), len(l.rules), strings.Join(ruleNames(l.rules), ", "))
}

func ruleNames(rules []Rule) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Name)
	}
	return out
}

func quoteList(names []string) string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, strconv.Quote(n))
	}
	return strings.Join(out, ", ")
}

// normalizeSniff strips a leading UTF-8 BOM and folds CRLF to LF so
// that simple '^...$' anchors in rule regexes work the same way
// whether the source file came from Windows or Unix.
func normalizeSniff(b []byte) []byte {
	b = bytes.TrimPrefix(b, []byte(utf8BOM))
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// Stream emits one Record per logical block.
//
// Coalescing rules:
//   - A line matching Separator closes the current record (if any) and
//     opens a new one.
//   - A line matching Field starts a new key/value pair.
//   - A non-empty line that matches neither, when a record is open, is
//     appended to the previous key's value with a leading newline.
//   - Lines before the first separator are ignored (file preamble).
func (l *Loader) Stream(ctx context.Context, h ingest.LoadHint, emit func(ingest.Record) error) error {
	rule, ok := l.pickRule(h)
	if !ok {
		return fmt.Errorf("block: no rule matched %s", h.Path)
	}

	f, err := os.Open(h.Path)
	if err != nil {
		return fmt.Errorf("block: open: %w", err)
	}
	defer f.Close()

	// Consume a leading UTF-8 BOM so reading starts on real content.
	// CRLF is handled inside ReadLineBounded which strips trailing \r.
	if err := skipBOM(f); err != nil {
		return fmt.Errorf("block: skip BOM: %w", err)
	}

	// bufio.Reader instead of bufio.Scanner because Scanner's
	// bufio.ErrTooLong is terminal — a single line above the buffer
	// cap would kill the rest of the ingest. ReadLineBounded truncates
	// the bad line, logs it, and resumes from the next line boundary.
	br := bufio.NewReaderSize(f, 64*1024)

	var rec map[string]string
	var lastKey string

	flush := func() error {
		if len(rec) == 0 {
			rec = nil
			lastKey = ""
			return nil
		}
		out := buildRecord(rule, rec, h.Path)
		rec = nil
		lastKey = ""
		return emit(out)
	}

	keyIdx := rule.Field.SubexpIndex("key")
	valIdx := rule.Field.SubexpIndex("value")

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, truncated, err := ingest.ReadLineBounded(br, ingest.MaxLineBytes)
		if err != nil && err != io.EOF {
			return fmt.Errorf("block: read: %w", err)
		}
		eof := err == io.EOF
		if line == "" && eof && !truncated {
			break
		}
		// Skip truncated lines: a Message field that genuinely runs to
		// many MiB would be appended via continuation, but if a single
		// physical line overflows the cap it's almost certainly binary
		// junk that we don't want polluting any record.
		if truncated {
			if eof {
				break
			}
			continue
		}

		if rule.Separator.MatchString(line) {
			if ferr := flush(); ferr != nil {
				return ferr
			}
			rec = map[string]string{}
		} else if rec != nil {
			if m := rule.Field.FindStringSubmatch(line); m != nil {
				key := strings.TrimRight(m[keyIdx], " \t")
				rec[key] = m[valIdx]
				lastKey = key
			} else if lastKey != "" && strings.TrimSpace(line) != "" {
				// Continuation: append to last key, preserving the
				// line break so multi-line Messages stay legible.
				rec[lastKey] = rec[lastKey] + "\n" + line
			}
		}
		// Preamble before first separator (rec == nil) is ignored.

		if eof {
			break
		}
	}
	return flush()
}

// pickRule re-evaluates which rule matches a file. Cheap (regex on the
// sniff buffer) and stateless, which keeps the Loader safe to use
// concurrently across files. When h.Sniff is empty (callers that build
// hints by hand instead of via ingest.HintForFile), reads the first
// SniffSize bytes from h.Path so direct Stream calls still work.
//
// The sniff buffer is normalized before matching: leading UTF-8 BOM is
// stripped and CRLF is folded to LF. This means rules can use plain
// '^...$' anchors without worrying about platform quirks.
func (l *Loader) pickRule(h ingest.LoadHint) (Rule, bool) {
	sniff := h.Sniff
	if len(sniff) == 0 && h.Path != "" {
		if f, err := os.Open(h.Path); err == nil {
			buf := make([]byte, ingest.SniffSize)
			n, _ := io.ReadFull(f, buf)
			sniff = buf[:n]
			f.Close()
		}
	}
	sniff = normalizeSniff(sniff)

	var best Rule
	bestPriority := -1
	for _, r := range l.rules {
		if r.Sniff.Match(sniff) && r.Priority > bestPriority {
			best = r
			bestPriority = r.Priority
		}
	}
	return best, bestPriority >= 0
}

// skipBOM reads (and discards) a leading UTF-8 BOM from f, or seeks
// back to start when the first three bytes aren't a BOM. After this
// call the file position is on the first real content byte.
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

// buildRecord turns the accumulated fields into a final ingest.Record:
// every key is copied verbatim, plus normalized @timestamp and
// _source_format markers.
func buildRecord(rule Rule, fields map[string]string, sourcePath string) ingest.Record {
	r := ingest.Record{}
	for k, v := range fields {
		r[k] = v
	}
	r[ingest.FieldSource] = "block:" + rule.Name

	for _, k := range rule.TsField {
		raw, ok := fields[k]
		if !ok {
			continue
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if t, err := parseAnyTime(raw, rule.TsLayouts); err == nil {
			r[ingest.FieldTimestamp] = t.UTC().Format(time.RFC3339Nano)
			break
		}
	}
	return r
}

func parseAnyTime(s string, layouts []string) (time.Time, error) {
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("no layout matched %q", s)
}

// compileRule validates a YAML doc and returns a runnable Rule.
//
// Required keys:  separator, field
// Optional keys:  name, priority, sniff, ts_field, ts_layouts
//
// `field` must contain named capturing groups "key" and "value" —
// without those the adapter has no way to extract record fields.
func compileRule(r ingest.RawRule) (Rule, error) {
	name := strOr(r.Data, "name", r.Name, "unnamed")

	sep := strOr(r.Data, "separator", "")
	if sep == "" {
		return Rule{}, fmt.Errorf("rule %q: separator is required", name)
	}
	field := strOr(r.Data, "field", "")
	if field == "" {
		return Rule{}, fmt.Errorf("rule %q: field is required", name)
	}

	sepRe, err := regexp.Compile(sep)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %q: separator regex: %w", name, err)
	}
	fieldRe, err := regexp.Compile(field)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %q: field regex: %w", name, err)
	}
	if fieldRe.SubexpIndex("key") < 0 || fieldRe.SubexpIndex("value") < 0 {
		return Rule{}, fmt.Errorf("rule %q: field regex must define named groups (?P<key>...) and (?P<value>...)", name)
	}

	// Default sniff = separator, in multi-line mode so '^' matches
	// line starts inside the sniff buffer.
	sniffPat := strOr(r.Data, "sniff", sep)
	sniffRe, err := regexp.Compile("(?m)" + sniffPat)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %q: sniff regex: %w", name, err)
	}

	return Rule{
		Name:      name,
		Priority:  intOr(r.Data, "priority", 70),
		Sniff:     sniffRe,
		Separator: sepRe,
		Field:     fieldRe,
		TsField:   strSliceOr(r.Data, "ts_field", []string{"Timestamp", "@timestamp"}),
		TsLayouts: strSliceOr(r.Data, "ts_layouts", []string{time.RFC3339Nano}),
	}, nil
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

func strSliceOr(m map[string]any, key string, def []string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return def
	}
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

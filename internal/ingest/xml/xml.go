// Package xml is the Loader for XML-shaped documents.
//
// Autodetect picks the "row element" — the element name that appears
// most often as siblings under a single parent in the first sample
// window. For:
//
//   - <PartioningInfo>…<SortedFrames>…<Frame …/>…</SortedFrames></…>
//     The row is <Frame> (thousands of siblings under SortedFrames).
//   - <BasicImagingBCs>…<ImagingObjects>…<ImagingObject …>…</…></…></…>
//     The row is <ImagingObject> (hundreds of siblings under ImagingObjects).
//   - Streams with no single root, like UtilizationEvents.txt:
//     <Event …>…</Event><Event …>…</Event>…
//     The row is <Event>.
//
// Each row's attributes and descendants are flattened into a single
// flat map with dot-paths:
//
//   - element attributes:                  parent.child.@attr_name = value
//   - row element's own attributes:        @attr_name = value
//   - leaf element text:                   parent.child = "text"
//
// Repeated paths (same key appearing more than once in a record) are
// collapsed into a list so information is never silently dropped. HTML-
// encoded XML inside leaf text is left as a string — encoding/xml has
// already decoded the entities, the result is just stored verbatim.
//
// Rules live in parsers.d/*.yaml. The adapter is product-agnostic: the
// row element name and parent paths are discovered, not configured.
package xml

import (
	"bytes"
	"context"
	stdxml "encoding/xml"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/labmk/obs-viewer/internal/ingest"
)

const utf8BOM = "\xef\xbb\xbf"

// minRowSiblings is the threshold the row-detector requires before
// declaring a winner. With fewer than this many siblings under any
// single parent, the file looks like one-off XML (a config doc, an
// envelope) rather than a record stream.
const minRowSiblings = 3

// sampleBytes is how much of the file the row-detector reads. Plenty
// for hundreds of rows in any of the observed samples without
// materializing huge files in memory.
const sampleBytes = 1 << 20 // 1 MiB

// Rule is one compiled XML rule loaded from YAML.
type Rule struct {
	Name         string
	Priority     int
	Sniff        *regexp.Regexp // matched against the (BOM-stripped) sniff buffer
	TsCandidates []string       // flattened key names to try in order for @timestamp
	TsLayouts    []string       // Go time layouts tried in order
}

// Loader is the XML adapter.
type Loader struct {
	rules []Rule
}

// New compiles raw YAML rules.
func New(raw []ingest.RawRule) (*Loader, error) {
	out := make([]Rule, 0, len(raw))
	for _, r := range raw {
		c, err := compileRule(r)
		if err != nil {
			return nil, fmt.Errorf("xml rule from %s: %w", r.Source, err)
		}
		out = append(out, c)
	}
	return &Loader{rules: out}, nil
}

// Name reports the loader name.
func (Loader) Name() string { return "xml" }

// Detect returns the priority of the first rule whose sniff regex
// matches the sniff buffer.
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

// Stream emits one Record per occurrence of the autodetected row
// element. Reads the file twice: once briefly to find the row element
// name, once fully to stream-decode each row.
func (l *Loader) Stream(ctx context.Context, h ingest.LoadHint, emit func(ingest.Record) error) error {
	rule, ok := l.pickRule(h)
	if !ok {
		return fmt.Errorf("xml: no rule matched %s", h.Path)
	}

	rowName, err := detectRowElement(h.Path)
	if err != nil {
		return fmt.Errorf("xml: row detect: %w", err)
	}
	if rowName == "" {
		return fmt.Errorf("xml: could not autodetect row element in %s", h.Path)
	}

	f, err := os.Open(h.Path)
	if err != nil {
		return fmt.Errorf("xml: open: %w", err)
	}
	defer f.Close()
	r, err := xmlReader(f)
	if err != nil {
		return fmt.Errorf("xml: prepare reader: %w", err)
	}

	dec := stdxml.NewDecoder(r)
	dec.Strict = false

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("xml: token: %w", err)
		}
		se, ok := tok.(stdxml.StartElement)
		if !ok || se.Name.Local != rowName {
			continue
		}
		root, err := parseElement(dec, se)
		if err != nil {
			return fmt.Errorf("xml: parse element %s: %w", rowName, err)
		}
		rec := ingest.Record{ingest.FieldSource: "xml:" + rowName}
		flatten(root, "", rec)
		applyTimestamp(rec, rule)
		if err := emit(rec); err != nil {
			return err
		}
	}
}

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

// element is the in-memory representation of one parsed XML element.
type element struct {
	Name     string
	Attrs    []stdxml.Attr
	Children []*element
	Text     strings.Builder
}

// parseElement decodes from d into an element until the matching
// EndElement is reached.
func parseElement(d *stdxml.Decoder, start stdxml.StartElement) (*element, error) {
	e := &element{Name: start.Name.Local, Attrs: start.Attr}
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case stdxml.StartElement:
			child, err := parseElement(d, t)
			if err != nil {
				return nil, err
			}
			e.Children = append(e.Children, child)
		case stdxml.EndElement:
			return e, nil
		case stdxml.CharData:
			e.Text.Write(t)
		}
	}
}

// flatten writes one entry per attribute and leaf-text into out using
// dot-path keys. Repeated keys are collapsed into a list to preserve
// every occurrence.
func flatten(e *element, prefix string, out ingest.Record) {
	for _, a := range e.Attrs {
		key := "@" + a.Name.Local
		if prefix != "" {
			key = prefix + ".@" + a.Name.Local
		}
		setOrAppend(out, key, a.Value)
	}
	for _, c := range e.Children {
		sub := c.Name
		if prefix != "" {
			sub = prefix + "." + c.Name
		}
		flatten(c, sub, out)
	}
	text := strings.TrimSpace(e.Text.String())
	if text == "" {
		return
	}
	// Only emit text for non-row elements. The row element's own text
	// is rarely meaningful (mixed-content rows are uncommon) and would
	// land at an unnamed top-level key.
	if prefix != "" {
		setOrAppend(out, prefix, text)
	}
}

func setOrAppend(out ingest.Record, key string, val string) {
	existing, ok := out[key]
	if !ok {
		out[key] = val
		return
	}
	switch v := existing.(type) {
	case string:
		out[key] = []any{v, val}
	case []any:
		out[key] = append(v, val)
	}
}

// applyTimestamp resolves @timestamp from the rule's candidate field
// list. The first candidate that exists, is a string, and parses
// against one of the rule's layouts wins.
func applyTimestamp(rec ingest.Record, rule Rule) {
	for _, cand := range rule.TsCandidates {
		raw, ok := rec[cand].(string)
		if !ok {
			// If the candidate ended up as a list (repeated path), take
			// the first element — usually fine for timestamps.
			if list, ok := rec[cand].([]any); ok && len(list) > 0 {
				if s, ok := list[0].(string); ok {
					raw = s
				}
			}
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		for _, layout := range rule.TsLayouts {
			if t, err := time.Parse(layout, raw); err == nil {
				rec[ingest.FieldTimestamp] = t.UTC().Format(time.RFC3339Nano)
				return
			}
		}
	}
}

// detectRowElement reads up to sampleBytes from path, parses it as XML
// (tolerantly, no single-root requirement), and returns the element
// name with the highest sibling count under any one parent.
func detectRowElement(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	r, err := xmlReader(f)
	if err != nil {
		return "", err
	}
	lr := &io.LimitedReader{R: r, N: int64(sampleBytes)}

	dec := stdxml.NewDecoder(lr)
	dec.Strict = false

	type frame struct {
		childCounts map[string]int
	}
	var stack []*frame
	type stats struct {
		maxSiblings int
		firstDepth  int
	}
	seen := map[string]*stats{}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Truncated sample: process what we have so far.
			break
		}
		switch t := tok.(type) {
		case stdxml.StartElement:
			name := t.Name.Local
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				top.childCounts[name]++
				count := top.childCounts[name]
				if s, ok := seen[name]; ok {
					if count > s.maxSiblings {
						s.maxSiblings = count
					}
				} else {
					seen[name] = &stats{maxSiblings: count, firstDepth: len(stack)}
				}
			}
			stack = append(stack, &frame{childCounts: map[string]int{}})
		case stdxml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}

	// Pick the element name with the highest sibling count. Ties go to
	// the element seen at the smallest depth — the outer "record" beats
	// inner sub-records (e.g. <Event> beats nested <Reference>).
	bestName := ""
	bestSiblings := minRowSiblings - 1
	bestDepth := 0
	for name, s := range seen {
		if s.maxSiblings < minRowSiblings {
			continue
		}
		if s.maxSiblings > bestSiblings ||
			(s.maxSiblings == bestSiblings && s.firstDepth < bestDepth) {
			bestName = name
			bestSiblings = s.maxSiblings
			bestDepth = s.firstDepth
		}
	}
	return bestName, nil
}

// xmlReader wraps f so that:
//   - a leading UTF-8 BOM is skipped (stdxml chokes on it otherwise)
//   - "loose" multi-root documents are accepted by wrapping the body
//     in a synthetic <obs_viewer_root>…</obs_viewer_root> when no
//     XML declaration or root element starts the file
//
// The wrapper element name has no semantic meaning — it's filtered out
// during streaming because we only emit on the autodetected row name.
func xmlReader(f *os.File) (io.Reader, error) {
	// Peek up to 64 bytes to decide whether to wrap.
	buf := make([]byte, 64)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	head := buf[:n]
	head = bytes.TrimPrefix(head, []byte(utf8BOM))
	trimmed := bytes.TrimLeft(head, " \t\r\n")
	rest := bytes.NewReader(head)
	combined := io.MultiReader(rest, f)

	// If the file already opens with <?xml or with a recognizable
	// single-root start tag (no immediate sibling), let the standard
	// decoder handle it. We unconditionally wrap in a synthetic root —
	// the cost is one extra StartElement/EndElement pair, but it makes
	// the loose multi-root case (UtilizationEvents.txt) Just Work.
	_ = trimmed
	pre := strings.NewReader("<obs_viewer_root>")
	post := strings.NewReader("</obs_viewer_root>")
	return io.MultiReader(pre, combined, post), nil
}

func normalizeSniff(b []byte) []byte {
	b = bytes.TrimPrefix(b, []byte(utf8BOM))
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

func compileRule(r ingest.RawRule) (Rule, error) {
	name := strOr(r.Data, "name", r.Name, "unnamed")
	sniffPat := strOr(r.Data, "sniff", `^\s*<`)
	sniffRe, err := regexp.Compile(sniffPat)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %q: sniff regex: %w", name, err)
	}
	return Rule{
		Name:         name,
		Priority:     intOr(r.Data, "priority", 80),
		Sniff:        sniffRe,
		TsCandidates: strSliceOr(r.Data, "ts_candidates", []string{"@time", "@Timestamp", "@timestamp", "Timestamp", "TimeStamp", "TimeStamp_Absolute"}),
		TsLayouts:    strSliceOr(r.Data, "ts_layouts", []string{time.RFC3339Nano, "2006-01-02 15:04:05Z07:00", "2006-01-02 15:04:05Z"}),
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

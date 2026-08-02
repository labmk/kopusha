package ingest

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// SniffSize is how many bytes from the start of the file are passed to
// each Loader.Detect for content sniffing. 512 covers every signature
// and the first XML element / first log line in all observed samples.
const SniffSize = 512

// Registry holds the set of registered loaders and picks one per file.
//
// Registration order is irrelevant — Pick sorts by confidence then by
// loader name so behavior is fully deterministic.
type Registry struct {
	loaders []Loader
}

// NewRegistry returns an empty Registry. Use Register to add loaders.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a Loader to the registry. Duplicate names are allowed
// but discouraged — Pick's tie-breaker rule would make one of them
// unreachable.
func (r *Registry) Register(l Loader) {
	r.loaders = append(r.loaders, l)
}

// Loaders returns a copy of the registered loaders, useful for tests
// and for /api/version-style introspection.
func (r *Registry) Loaders() []Loader {
	out := make([]Loader, len(r.loaders))
	copy(out, r.loaders)
	return out
}

// HintForFile builds a LoadHint by stat'ing the file and reading the
// first SniffSize bytes. Returns an error only on stat/open failure;
// a short file (less than SniffSize) is normal and not an error.
func HintForFile(path string) (LoadHint, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return LoadHint{}, fmt.Errorf("abs path: %w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return LoadHint{}, fmt.Errorf("stat: %w", err)
	}
	f, err := os.Open(abs)
	if err != nil {
		return LoadHint{}, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	buf := make([]byte, SniffSize)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return LoadHint{}, fmt.Errorf("sniff read: %w", err)
	}
	buf = buf[:n]

	return LoadHint{
		Path:  abs,
		Ext:   strings.ToLower(filepath.Ext(abs)),
		Sniff: buf,
		MTime: st.ModTime(),
	}, nil
}

// Pick returns the highest-confidence loader for h. Returns nil when
// no loader reports a score greater than zero.
//
// Tie-breaker: when two loaders return identical scores, the one with
// the lexicographically smaller Name() wins. This keeps the choice
// stable across registration orders and makes "which loader handled
// this file?" reproducible in logs.
func (r *Registry) Pick(h LoadHint) Loader {
	type scored struct {
		score int
		l     Loader
	}
	all := make([]scored, 0, len(r.loaders))
	for _, l := range r.loaders {
		s := l.Detect(h)
		if s > 0 {
			all = append(all, scored{s, l})
		}
	}
	if len(all) == 0 {
		return nil
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].l.Name() < all[j].l.Name()
	})
	return all[0].l
}

// AdapterVerdict is one adapter's answer about one file: the score it
// returned from Detect, and — when the adapter implements Explainer —
// why.
type AdapterVerdict struct {
	Name   string `json:"name"`
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

// Diagnosis is the whole story of a dispatch decision, including the
// parts Pick throws away.
//
// Pick reduces every adapter's opinion to a single winner, which is the
// right behavior for loading a file and the wrong one for explaining a
// failure. When nothing matches, "no loader matched" tells the operator
// only that obs-viewer is unhappy — not which adapters looked, what
// each objected to, or what the first line actually looked like by the
// time it reached the parser. Diagnosis carries all of it.
//
// The encoding notes matter more than they look. A UTF-16 file, a file
// with a BOM, and a file with CRLF endings all *look* correct in an
// editor and all fail a regex anchored with ^ or $ for reasons that are
// invisible on screen.
type Diagnosis struct {
	Path string `json:"path"`
	// Chosen is the adapter Pick would return, or "" when nothing
	// scored above zero.
	Chosen    string           `json:"chosen"`
	BestScore int              `json:"best_score"`
	Adapters  []AdapterVerdict `json:"adapters"`
	// FirstLine is the first non-blank line as the parser sees it:
	// after BOM removal and CRLF folding, truncated.
	FirstLine string `json:"first_line"`
	// Notes are encoding and content observations — BOM, CRLF, NUL
	// bytes, invalid UTF-8, empty file.
	Notes []string `json:"notes"`
}

// Explain runs every registered adapter's Detect and collects the
// results, in the same order Pick would rank them, alongside each
// adapter's reason and a description of what the file's first line
// looks like on the wire.
//
// Unlike Pick this keeps adapters that scored zero — they are the whole
// point. Ranking is identical to Pick's (score descending, then name)
// so the first entry is always the adapter that would have won.
func (r *Registry) Explain(h LoadHint) Diagnosis {
	d := Diagnosis{Path: h.Path}

	d.Adapters = make([]AdapterVerdict, 0, len(r.loaders))
	for _, l := range r.loaders {
		v := AdapterVerdict{Name: l.Name(), Score: l.Detect(h)}
		if ex, ok := l.(Explainer); ok {
			v.Reason = ex.Explain(h)
		}
		d.Adapters = append(d.Adapters, v)
	}
	sort.SliceStable(d.Adapters, func(i, j int) bool {
		if d.Adapters[i].Score != d.Adapters[j].Score {
			return d.Adapters[i].Score > d.Adapters[j].Score
		}
		return d.Adapters[i].Name < d.Adapters[j].Name
	})
	if len(d.Adapters) > 0 && d.Adapters[0].Score > 0 {
		d.Chosen = d.Adapters[0].Name
		d.BestScore = d.Adapters[0].Score
	}

	d.FirstLine, d.Notes = DescribeSniff(h.Sniff)
	return d
}

// firstLineMax caps the reported first line. Long enough to show a full
// log line with a stack-frame prefix; short enough that a minified JSON
// blob or a base64 payload doesn't fill the screen.
const firstLineMax = 300

// DescribeSniff returns the first non-blank line of b as a parser would
// see it, plus notes about anything in the bytes that commonly breaks
// parsing but is invisible in an editor.
//
// Exported because the rule builder shows the same treatment for a
// pasted sample: the reason a hand-written regex "obviously matches"
// but doesn't is nearly always one of these.
func DescribeSniff(b []byte) (firstLine string, notes []string) {
	if len(b) == 0 {
		return "", []string{"file is empty"}
	}

	switch {
	case bytes.HasPrefix(b, []byte("\xef\xbb\xbf")):
		notes = append(notes, "starts with a UTF-8 byte-order mark (skipped by the line and block adapters)")
		b = b[3:]
	case bytes.HasPrefix(b, []byte("\xff\xfe")):
		notes = append(notes, "starts with a UTF-16 LE byte-order mark — convert to UTF-8 before loading")
	case bytes.HasPrefix(b, []byte("\xfe\xff")):
		notes = append(notes, "starts with a UTF-16 BE byte-order mark — convert to UTF-8 before loading")
	}

	if bytes.Contains(b, []byte("\r\n")) {
		notes = append(notes, "CRLF line endings (folded to LF before matching)")
	}
	if bytes.IndexByte(b, 0) >= 0 {
		notes = append(notes, "contains NUL bytes — binary, or UTF-16 without a byte-order mark")
	}
	if !utf8.Valid(b) {
		notes = append(notes, "not valid UTF-8 — bytes are passed through unchanged, so regexes over non-ASCII text may not match")
	}

	folded := bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	for _, ln := range bytes.Split(folded, []byte("\n")) {
		if len(bytes.TrimSpace(ln)) == 0 {
			continue
		}
		firstLine = string(ln)
		break
	}
	if firstLine == "" && len(notes) == 0 {
		notes = append(notes, "no non-blank line in the first "+fmt.Sprint(len(b))+" bytes")
	}
	if len(firstLine) > firstLineMax {
		firstLine = strings.ToValidUTF8(firstLine[:firstLineMax], "") + "…"
	}
	return firstLine, notes
}

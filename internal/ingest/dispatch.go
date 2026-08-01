package ingest

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

package parsers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/labmk/kopusha/internal/ingest"
	"github.com/labmk/kopusha/internal/ingest/block"
	"github.com/labmk/kopusha/internal/ingest/evtx"
	"github.com/labmk/kopusha/internal/ingest/line"
	"github.com/labmk/kopusha/internal/ingest/ndjson"
	"github.com/labmk/kopusha/internal/ingest/parquet"
	ingestxml "github.com/labmk/kopusha/internal/ingest/xml"
)

// Manager owns parsers.d: it builds the loader registry from the rules
// on disk, rebuilds it when they change, and is the only writer.
//
// Building the registry lives here rather than in main because a rule
// saved from the UI has to take effect immediately. Restarting to pick
// up a rule you just wrote would undo most of what the rule builder is
// for — the loop is paste, preview, save, load the file, and a restart
// in the middle of it means losing the loaded files you were trying to
// parse in the first place.
type Manager struct {
	dir   string
	apply func(*ingest.Registry)

	mu    sync.RWMutex
	reg   *ingest.Registry
	stats Stats
}

// Stats describes what the last Reload found.
type Stats struct {
	Block int `json:"block"`
	Line  int `json:"line"`
	XML   int `json:"xml"`
	// Unknown carries rules whose `family:` matched no adapter, so a
	// typo there is reported rather than silently dropping the rule.
	Unknown []UnknownRule `json:"unknown,omitempty"`
}

// UnknownRule is a rule file with an unrecognized family.
type UnknownRule struct {
	Source string `json:"source"`
	Family string `json:"family"`
	Name   string `json:"name"`
}

// Info describes one rule as the UI lists it.
type Info struct {
	Name     string `json:"name"`
	Family   string `json:"family"`
	Priority int    `json:"priority"`
	File     string `json:"file"`
}

// ErrRuleExists is returned by Save when a rule file of that name is
// already on disk and overwrite was not requested.
var ErrRuleExists = errors.New("a rule with that name already exists")

// NewManager returns a Manager for dir. apply is called with the new
// registry after every successful Reload — normally engine.SetLoaders.
func NewManager(dir string, apply func(*ingest.Registry)) *Manager {
	return &Manager{dir: dir, apply: apply}
}

// Dir reports the parsers.d directory.
func (m *Manager) Dir() string { return m.dir }

// Registry returns the current loader registry.
func (m *Manager) Registry() *ingest.Registry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reg
}

// Stats returns what the last Reload found.
func (m *Manager) Stats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stats
}

// Reload rebuilds the registry from the rules currently on disk.
//
// A malformed rule fails the whole reload and leaves the previous
// registry in place. Half-applying a rule set would leave the engine in
// a state that matches nothing on disk, and the operator would be
// debugging a configuration that does not exist anywhere.
func (m *Manager) Reload() (Stats, error) {
	ruleSet, err := ingest.LoadRules(m.dir)
	if err != nil {
		return Stats{}, err
	}

	blockLoader, err := block.New(ruleSet.Block)
	if err != nil {
		return Stats{}, fmt.Errorf("block adapter: %w", err)
	}
	lineLoader, err := line.New(ruleSet.Line)
	if err != nil {
		return Stats{}, fmt.Errorf("line adapter: %w", err)
	}
	xmlLoader, err := ingestxml.New(ruleSet.XML)
	if err != nil {
		return Stats{}, fmt.Errorf("xml adapter: %w", err)
	}

	reg := ingest.NewRegistry()
	reg.Register(ndjson.New())
	reg.Register(parquet.New())
	reg.Register(evtx.New())
	reg.Register(blockLoader)
	reg.Register(lineLoader)
	reg.Register(xmlLoader)

	stats := Stats{
		Block: len(ruleSet.Block),
		Line:  len(ruleSet.Line),
		XML:   len(ruleSet.XML),
	}
	for _, r := range ruleSet.Other {
		stats.Unknown = append(stats.Unknown, UnknownRule{
			Source: r.Source, Family: r.Family, Name: r.Name,
		})
	}

	m.mu.Lock()
	m.reg = reg
	m.stats = stats
	m.mu.Unlock()

	if m.apply != nil {
		m.apply(reg)
	}
	return stats, nil
}

// List returns the rules on disk, sorted by family then name.
func (m *Manager) List() ([]Info, error) {
	ruleSet, err := ingest.LoadRules(m.dir)
	if err != nil {
		return nil, err
	}
	var out []Info
	add := func(rules []ingest.RawRule, family string) {
		for _, r := range rules {
			info := Info{
				Name:   r.Name,
				Family: family,
				File:   filepath.Base(r.Source),
			}
			switch v := r.Data["priority"].(type) {
			case int:
				info.Priority = v
			case float64:
				info.Priority = int(v)
			}
			out = append(out, info)
		}
	}
	add(ruleSet.Block, "block")
	add(ruleSet.Line, "line")
	add(ruleSet.XML, "xml")
	sort.Slice(out, func(i, j int) bool {
		if out[i].Family != out[j].Family {
			return out[i].Family < out[j].Family
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// rulePrefix orders user-written rule files after the ones that ship
// with the binary. Cosmetic — precedence is decided by each rule's
// priority, not by filename — but it keeps a directory listing
// readable, which is where anyone debugging a rule starts.
const rulePrefix = "50-line-"

// Save writes the draft to parsers.d and reloads the registry.
//
// The rule name becomes a filename, so it is normalized through Slug
// first and the result is joined to the directory and then checked to
// be inside it. The second check is the one that counts: it does not
// depend on Slug being exhaustive, so a name that somehow survives
// normalization still cannot write outside parsers.d.
func (m *Manager) Save(d Draft, overwrite bool) (string, error) {
	slug, err := Slug(d.Name)
	if err != nil {
		return "", err
	}
	d.Name = slug
	if err := d.Validate(); err != nil {
		return "", err
	}

	absDir, err := filepath.Abs(m.dir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(absDir, rulePrefix+slug+".yaml")
	if !strings.HasPrefix(path, absDir+string(filepath.Separator)) {
		return "", fmt.Errorf("rule name %q does not produce a path inside %s", d.Name, m.dir)
	}

	if _, err := os.Stat(path); err == nil && !overwrite {
		return "", fmt.Errorf("%w: %s", ErrRuleExists, filepath.Base(path))
	}

	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", m.dir, err)
	}
	if err := os.WriteFile(path, d.YAML(), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}

	// A rule that cannot be loaded must not be left behind: it would
	// make the next start fail with a fatal parse error, which is a
	// much worse outcome than a rejected save. Validate ran before the
	// write, so reaching this is a bug rather than bad input — but the
	// consequence is a binary that will not boot, so it is worth
	// undoing rather than trusting.
	if _, err := m.Reload(); err != nil {
		os.Remove(path)
		if _, rerr := m.Reload(); rerr != nil {
			return "", fmt.Errorf("rule rejected (%v); reverting also failed: %w", err, rerr)
		}
		return "", fmt.Errorf("rule rejected and not saved: %w", err)
	}
	return path, nil
}

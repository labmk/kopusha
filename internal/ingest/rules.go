package ingest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// RuleSet is the merged set of YAML rule documents loaded from
// parsers.d/. It carries one slice per format family; each family's
// adapter consumes only its own slice.
//
// Rules are typed loosely (map[string]any) at this layer because the
// adapters know best how to validate their own fields. Validation
// errors surface from adapter constructors (e.g. line.New(rules.Line))
// rather than at YAML-parse time.
type RuleSet struct {
	Block []RawRule
	XML   []RawRule
	Line  []RawRule
	// Other carries rules with an unknown family — recorded so a typo
	// in `family:` doesn't silently vanish.
	Other []RawRule
}

// RawRule is a single YAML document with its source file path attached
// for error reporting.
type RawRule struct {
	Source string         // file path the rule was loaded from
	Family string         // value of the top-level `family:` key
	Name   string         // value of the top-level `name:` key
	Data   map[string]any // full document
}

// LoadRules walks dir, parses every *.yaml / *.yml file (non-recursive,
// hidden files skipped), and merges all rules into one RuleSet.
//
// Files are loaded in lexicographic order so a deterministic precedence
// can be established by naming convention (e.g. 00-defaults.yaml,
// 10-product-x.yaml). Within each family, the adapter is free to sort
// by an explicit `priority:` field.
//
// Missing dir is not an error — returns an empty RuleSet. This lets
// the binary start even when parsers.d/ wasn't shipped, and falls back
// to whatever each adapter does with an empty rule slice (typically:
// nothing detected).
func LoadRules(dir string) (*RuleSet, error) {
	rs := &RuleSet{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return rs, nil
		}
		return nil, fmt.Errorf("read parsers dir %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, ".") {
			continue
		}
		l := strings.ToLower(n)
		if !strings.HasSuffix(l, ".yaml") && !strings.HasSuffix(l, ".yml") {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)

	for _, n := range names {
		p := filepath.Join(dir, n)
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		if doc == nil {
			continue
		}
		fam, _ := doc["family"].(string)
		name, _ := doc["name"].(string)
		rule := RawRule{Source: p, Family: fam, Name: name, Data: doc}
		switch fam {
		case "block":
			rs.Block = append(rs.Block, rule)
		case "xml":
			rs.XML = append(rs.XML, rule)
		case "line":
			rs.Line = append(rs.Line, rule)
		default:
			rs.Other = append(rs.Other, rule)
		}
	}
	return rs, nil
}

// Package config parses kopusha.conf — an INI-with-sections format.
//
// Top-level keys (no preceding [section]) are flat config: port, timeout,
// listen. These keep the format compatible with pre-0.4 kopusha.conf
// files that had no sections at all.
//
// Sections introduce module config: one [<module-name>] section per
// optional sub-feature. The module registry consults Section /
// ModuleEnabled to decide what to mount.
//
// Format rules:
//   - # or ; starts a comment line (full-line only; no inline comments).
//   - Blank lines are ignored.
//   - Whitespace around keys, values, and section names is trimmed.
//   - Section headers look like "[name]" on their own line.
//   - Keys before any section header live in the flat top-level map.
//   - Duplicate keys: last write wins. Duplicate section headers: merged.
//   - Module is enabled iff its section exists AND its "enabled" key, if
//     present, is not "false" / "0" / empty. Section present, no enabled
//     key ⇒ enabled.
package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	flat     map[string]string
	sections map[string]map[string]string
}

// Load reads path. Missing file is not an error — returns an empty Config
// so callers can treat "no conf file" the same as "conf file present but
// nothing set".
//
// Single-file API kept for backward compat and tests. Production code in
// 0.5.17+ uses LoadAll to merge the split-by-module conf layout.
func Load(path string) (*Config, error) {
	cfg := &Config{
		flat:     map[string]string{},
		sections: map[string]map[string]string{},
	}
	if err := mergeInto(cfg, path); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadAll loads every path into a single merged Config. Later-loaded
// files override earlier ones on flat-key collisions; section content
// merges (each subsequent file's [section] entries are added on top of
// the existing map). Missing files are skipped silently.
//
// 0.5.17 split kopusha.conf into per-module siblings — core in
// kopusha.conf, optional modules in kopusha_<module>.conf —
// so an operator can copy / drop / .example individual module configs
// without editing one big file. main.go globs them in sorted order
// (kopusha.conf first alphabetically, then branding, then export…)
// and passes the result here.
func LoadAll(paths []string) (*Config, error) {
	cfg := &Config{
		flat:     map[string]string{},
		sections: map[string]map[string]string{},
	}
	for _, p := range paths {
		if err := mergeInto(cfg, p); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// mergeInto parses one INI-style file and folds its contents into cfg.
// Extracted so Load (single-file) and LoadAll (multi-file) share the
// exact same parser semantics. Missing file is not an error.
func mergeInto(cfg *Config, path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	current := "" // "" = top-level (flat)
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") && len(line) >= 3 {
			current = strings.TrimSpace(line[1 : len(line)-1])
			if _, ok := cfg.sections[current]; !ok {
				cfg.sections[current] = map[string]string{}
			}
			continue
		}
		i := strings.Index(line, "=")
		if i <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:i])
		v := strings.TrimSpace(line[i+1:])
		if current == "" {
			cfg.flat[k] = v
		} else {
			cfg.sections[current][k] = v
		}
	}
	return s.Err()
}

// Get returns a top-level (flat) value, empty string if not set.
func (c *Config) Get(key string) string { return c.flat[key] }

// GetDefault returns a top-level value, or def when unset.
func (c *Config) GetDefault(key, def string) string {
	if v, ok := c.flat[key]; ok {
		return v
	}
	return def
}

// GetInt parses a top-level integer, returning def when missing or invalid.
func (c *Config) GetInt(key string, def int) int {
	if v, ok := c.flat[key]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// Section returns a section's key/value map and whether the section
// header was present in the file. A present-but-empty section returns
// (empty map, true).
func (c *Config) Section(name string) (map[string]string, bool) {
	s, ok := c.sections[name]
	return s, ok
}

// HasSection reports whether the named section header appeared in the file.
func (c *Config) HasSection(name string) bool {
	_, ok := c.sections[name]
	return ok
}

// ModuleEnabled implements the proposal's policy: a module is enabled
// when its section exists AND, if "enabled" is set, the value is not
// false/0/empty. Section present with no "enabled" key is treated as
// enabled — explicit opt-in by adding the section is the signal.
func (c *Config) ModuleEnabled(name string) bool {
	s, ok := c.sections[name]
	if !ok {
		return false
	}
	v, present := s["enabled"]
	if !present {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// FlatKeys returns the count of top-level keys — useful for the existing
// "Config loaded: N keys" startup log line.
func (c *Config) FlatKeys() int { return len(c.flat) }

// Sections returns the set of section names present, in no defined order.
func (c *Config) Sections() []string {
	out := make([]string, 0, len(c.sections))
	for k := range c.sections {
		out = append(out, k)
	}
	return out
}

package parsers

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labmk/kopusha/internal/ingest"
)

func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	var applied int
	m := NewManager(dir, func(*ingest.Registry) { applied++ })
	if _, err := m.Reload(); err != nil {
		t.Fatalf("initial reload: %v", err)
	}
	return m, dir
}

// A saved rule has to be live immediately. Needing a restart would undo
// most of what the builder is for: the operator would lose the loaded
// files they were trying to parse.
func TestSaveTakesEffectWithoutRestart(t *testing.T) {
	m, dir := newTestManager(t)

	sample := "2026-03-25 00:31:11.231 INFO something happened"
	if hint := hintFor(t, sample); m.Registry().Pick(hint) != nil {
		t.Fatal("a loader claimed the file before any rule was saved")
	}

	d := Suggest(sample + "\n2026-03-25 00:31:12.400 WARN something else")
	d.Name = "My Test Format"
	path, err := m.Save(d, false)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := filepath.Dir(path); got != dir {
		t.Errorf("wrote to %s, want a file in %s", got, dir)
	}

	loader := m.Registry().Pick(hintFor(t, sample))
	if loader == nil {
		t.Fatal("the saved rule did not take effect in the live registry")
	}
	if loader.Name() != "line" {
		t.Errorf("picked %s, want line", loader.Name())
	}
}

// The name becomes a filename, and it arrives from a browser.
func TestSaveRejectsPathTraversal(t *testing.T) {
	m, dir := newTestManager(t)
	d := Suggest("2026-03-25 00:31:11.231 INFO a\n2026-03-25 00:31:12.400 WARN b")

	for _, name := range []string{
		"../escape",
		"../../etc/passwd",
		"/absolute/path",
		`..\windows`,
		"C:\\Windows\\System32\\rule",
		".",
		"..",
		"",
		"   ",
		"...",
		"/",
	} {
		t.Run(name, func(t *testing.T) {
			d.Name = name
			path, err := m.Save(d, false)
			if err == nil {
				// A name that normalizes to something harmless is
				// acceptable; a write outside parsers.d is not.
				abs, _ := filepath.Abs(path)
				absDir, _ := filepath.Abs(dir)
				if !strings.HasPrefix(abs, absDir+string(filepath.Separator)) {
					t.Fatalf("wrote outside the rules directory: %s", abs)
				}
				t.Logf("normalized to %s", filepath.Base(path))
				return
			}
		})
	}

	// Whatever happened above, nothing may exist outside the directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "..") || strings.ContainsAny(e.Name(), `/\`) {
			t.Errorf("suspicious file name written: %q", e.Name())
		}
	}
}

func TestSlugNormalizesAndRejects(t *testing.T) {
	ok := map[string]string{
		"My Test Format":  "my-test-format",
		"gateway_log":     "gateway-log",
		"APP  ✱  v2":      "app-v2",
		"already-a-slug":  "already-a-slug",
		"trailing---dash": "trailing-dash",
		"../escape":       "escape",
	}
	for in, want := range ok {
		got, err := Slug(in)
		if err != nil {
			t.Errorf("Slug(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"", "   ", "...", "///", "✱✱✱"} {
		if got, err := Slug(in); err == nil {
			t.Errorf("Slug(%q) = %q, want an error", in, got)
		}
	}
}

// Overwriting someone's rule needs to be asked for, not assumed.
func TestSaveRefusesToClobber(t *testing.T) {
	m, _ := newTestManager(t)
	d := Suggest("2026-03-25 00:31:11.231 INFO a\n2026-03-25 00:31:12.400 WARN b")
	d.Name = "dupe"

	if _, err := m.Save(d, false); err != nil {
		t.Fatalf("first save: %v", err)
	}
	_, err := m.Save(d, false)
	if !errors.Is(err, ErrRuleExists) {
		t.Fatalf("second save error = %v, want ErrRuleExists", err)
	}
	if _, err := m.Save(d, true); err != nil {
		t.Fatalf("save with overwrite: %v", err)
	}
}

// The saved YAML has to be readable by the loader that reads shipped
// rules — the file is an ordinary rule file, and the operator will edit
// it by hand.
func TestSavedYAMLRoundTrips(t *testing.T) {
	m, dir := newTestManager(t)

	sample := `app: 26.03.26 09:55:01:280 36800/113264 creation started
svc: 26.03.26 09:55:02:100 36800/98 request handled`
	d := Suggest(sample)
	d.Name = "round-trip"
	if _, err := m.Save(d, false); err != nil {
		t.Fatalf("save: %v", err)
	}

	rs, err := ingest.LoadRules(dir)
	if err != nil {
		t.Fatalf("reload from disk: %v", err)
	}
	if len(rs.Line) != 1 {
		t.Fatalf("got %d line rules on disk, want 1", len(rs.Line))
	}
	got := rs.Line[0]
	if got.Name != "round-trip" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Data["parse"] != d.Parse {
		t.Errorf("parse regex changed through YAML:\n  wrote %q\n  read  %q", d.Parse, got.Data["parse"])
	}
	if got.Data["ts_layout"] != d.TsLayout {
		t.Errorf("ts_layout = %v, want %q", got.Data["ts_layout"], d.TsLayout)
	}
	// The substitution this format needs must have survived too.
	subs, ok := got.Data["ts_regex_subs"].([]any)
	if !ok || len(subs) != 1 {
		t.Fatalf("ts_regex_subs did not round-trip: %#v", got.Data["ts_regex_subs"])
	}
}

// A regex is mostly backslashes, and a quoting bug there is silent:
// the rule loads and matches nothing.
func TestYAMLQuotingSurvivesRegexMetacharacters(t *testing.T) {
	d := Draft{
		Name:     "quoting",
		Parse:    `^(?P<ts>\d{2}) it's \\ a 'quoted' \[thing\] (?P<message>.*)$`,
		TsLayout: "15",
	}
	dir := t.TempDir()
	m := NewManager(dir, nil)
	if _, err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Save(d, false); err != nil {
		t.Fatalf("save: %v", err)
	}
	rs, err := ingest.LoadRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := rs.Line[0].Data["parse"]; got != d.Parse {
		t.Errorf("regex mangled by YAML quoting:\n  wrote %q\n  read  %q", d.Parse, got)
	}
}

// A rule that cannot be compiled must not be left on disk: the next
// start reads parsers.d and dies on it.
func TestSaveDoesNotLeaveABrokenRuleBehind(t *testing.T) {
	m, dir := newTestManager(t)
	d := Draft{
		Name:     "broken",
		Parse:    `^(?P<ts>\d+(?P<message>.*)$`, // unbalanced
		TsLayout: "15:04:05",
	}
	if _, err := m.Save(d, false); err == nil {
		t.Fatal("saved a rule with an invalid regex")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("left %d file(s) behind: %v", len(entries), entries)
	}
}

func TestListReportsSavedRules(t *testing.T) {
	m, _ := newTestManager(t)
	d := Suggest("2026-03-25 00:31:11.231 INFO a\n2026-03-25 00:31:12.400 WARN b")
	d.Name = "listed"
	d.Priority = 77
	if _, err := m.Save(d, false); err != nil {
		t.Fatal(err)
	}
	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d rules, want 1", len(list))
	}
	if list[0].Name != "listed" || list[0].Family != "line" || list[0].Priority != 77 {
		t.Errorf("unexpected entry: %+v", list[0])
	}
}

func hintFor(t *testing.T, content string) ingest.LoadHint {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.log")
	if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := ingest.HintForFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

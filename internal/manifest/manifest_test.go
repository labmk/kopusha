package manifest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeRules(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestBuildRenderParseRoundTrip(t *testing.T) {
	dir := writeRules(t, map[string]string{
		"20-a.yaml": "family: line\n",
		"10-b.yaml": "family: block\n",
	})
	built, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(built.Render())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 2 {
		t.Fatalf("got %d entries, want 2", len(parsed))
	}
	for name, sum := range built {
		if parsed[name] != sum {
			t.Errorf("%s: %q != %q", name, parsed[name], sum)
		}
	}
}

// Regenerating must not produce a diff, or every build dirties the tree.
func TestRenderIsStable(t *testing.T) {
	dir := writeRules(t, map[string]string{
		"c.yaml": "3", "a.yaml": "1", "b.yaml": "2",
	})
	m, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	first := string(m.Render())
	for i := 0; i < 5; i++ {
		if got := string(m.Render()); got != first {
			t.Fatal("Render is not deterministic")
		}
	}
	// And sorted, so the diff of a real change stays readable.
	var names []string
	for _, line := range strings.Split(first, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		names = append(names, strings.Fields(line)[1])
	}
	want := []string{"a.yaml", "b.yaml", "c.yaml"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order = %v, want %v", names, want)
		}
	}
}

func TestCompareClassifiesEveryCase(t *testing.T) {
	dir := writeRules(t, map[string]string{
		"kept.yaml":     "original",
		"edited.yaml":   "original",
		"theirown.yaml": "user wrote this",
	})
	m, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Make it look like a shipped set: theirown.yaml was never shipped,
	// gone.yaml was shipped and then deleted by the user.
	delete(m, "theirown.yaml")
	m["gone.yaml"] = strings.Repeat("a", 64)

	// The user edits one shipped rule after install.
	if err := os.WriteFile(filepath.Join(dir, "edited.yaml"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := m.Compare(dir)
	if err != nil {
		t.Fatal(err)
	}
	eq := func(label string, got, want []string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s = %v, want %v", label, got, want)
		}
	}
	eq("Unchanged", d.Unchanged, []string{"kept.yaml"})
	eq("Modified", d.Modified, []string{"edited.yaml"})
	eq("Removed", d.Removed, []string{"gone.yaml"})
	eq("Added", d.Added, []string{"theirown.yaml"})

	if d.Clean() {
		t.Error("Clean() = true on a diverged directory")
	}
	if s := d.Summary(); !strings.Contains(s, "edited.yaml") || !strings.Contains(s, "1 modified") {
		t.Errorf("Summary() = %q", s)
	}
}

func TestCompareCleanWhenUntouched(t *testing.T) {
	dir := writeRules(t, map[string]string{"a.yaml": "x", "b.yaml": "y"})
	m, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := m.Compare(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Clean() {
		t.Errorf("Clean() = false for an untouched directory: %+v", d)
	}
	if d.Summary() != "" {
		t.Errorf("Summary() = %q, want empty", d.Summary())
	}
}

// Running with no parsers.d/ is supported — NDJSON and EVTX need no
// rules — so a missing directory reports state rather than failing.
func TestCompareTreatsMissingDirAsAllRemoved(t *testing.T) {
	m := Manifest{"a.yaml": strings.Repeat("b", 64)}
	d, err := m.Compare(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing dir must not error: %v", err)
	}
	if len(d.Removed) != 1 || d.Removed[0] != "a.yaml" {
		t.Errorf("Removed = %v", d.Removed)
	}
}

func TestCompareIgnoresNonRuleFiles(t *testing.T) {
	dir := writeRules(t, map[string]string{
		"rule.yaml":  "x",
		"README.md":  "not a rule",
		"notes.txt":  "not a rule",
		"backup.bak": "not a rule",
	})
	m, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 {
		t.Fatalf("Build picked up non-rule files: %v", m)
	}
	d, err := m.Compare(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Clean() {
		t.Errorf("stray files were classified as rules: %+v", d)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for name, body := range map[string]string{
		"one field":     "deadbeef\n",
		"three fields":  "aa bb cc\n",
		"short hash":    "abc  a.yaml\n",
		"not hex":       strings.Repeat("z", 64) + "  a.yaml\n",
		"missing hash":  "  a.yaml\n",
		"hash too long": strings.Repeat("a", 65) + "  x.yaml\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(body)); err == nil {
				t.Error("want an error, got nil")
			}
		})
	}
}

func TestParseSkipsCommentsAndBlanks(t *testing.T) {
	m, err := Parse([]byte("# a comment\n\n   \n" + strings.Repeat("a", 64) + "  x.yaml\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m["x.yaml"] == "" {
		t.Errorf("got %v", m)
	}
}

// The committed manifest must describe the committed parsers.d/. If this
// fails, someone changed a rule without regenerating — run ./build.sh, or
// `go generate ./...` once that exists. Left stale, the shipped binary
// would misjudge which rules the user edited.
func TestCommittedManifestIsCurrent(t *testing.T) {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot resolve source path")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(self)))

	committed, err := os.ReadFile(filepath.Join(root, "parsers.d.sha256"))
	if err != nil {
		t.Fatalf("parsers.d.sha256 missing — build.sh generates it: %v", err)
	}
	want, err := Build(filepath.Join(root, "parsers.d"))
	if err != nil {
		t.Fatal(err)
	}
	if string(committed) != string(want.Render()) {
		t.Errorf("parsers.d.sha256 is stale; regenerate it with ./build.sh\n"+
			"had:\n%s\nwant:\n%s", committed, want.Render())
	}
}

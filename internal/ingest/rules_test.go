package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRulesMissingDirReturnsEmpty(t *testing.T) {
	rs, err := LoadRules(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("expected nil error for missing dir, got %v", err)
	}
	if rs == nil {
		t.Fatal("nil ruleset")
	}
	if len(rs.Block)+len(rs.XML)+len(rs.Line)+len(rs.Other) != 0 {
		t.Fatalf("expected empty ruleset, got %+v", rs)
	}
}

func TestLoadRulesDispatchesByFamily(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("10-line.yaml", "family: line\nname: iso\nparse: 'x'\n")
	write("20-block.yaml", "family: block\nname: dash\nseparator: '----'\n")
	write("30-xml.yaml", "family: xml\nname: events\n")
	write("40-misc.yaml", "family: weird\nname: x\n")
	write(".hidden.yaml", "family: line\nname: should-be-skipped\n")
	write("readme.txt", "ignored")

	rs, err := LoadRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Line) != 1 || rs.Line[0].Name != "iso" {
		t.Errorf("Line dispatch wrong: %+v", rs.Line)
	}
	if len(rs.Block) != 1 || rs.Block[0].Name != "dash" {
		t.Errorf("Block dispatch wrong: %+v", rs.Block)
	}
	if len(rs.XML) != 1 || rs.XML[0].Name != "events" {
		t.Errorf("XML dispatch wrong: %+v", rs.XML)
	}
	if len(rs.Other) != 1 || rs.Other[0].Family != "weird" {
		t.Errorf("Other dispatch wrong: %+v", rs.Other)
	}
}

func TestLoadRulesSortedLexicographically(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("20-b.yaml", "family: line\nname: b\n")
	write("10-a.yaml", "family: line\nname: a\n")
	write("30-c.yaml", "family: line\nname: c\n")

	rs, err := LoadRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{rs.Line[0].Name, rs.Line[1].Name, rs.Line[2].Name}
	want := []string{"a", "b", "c"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

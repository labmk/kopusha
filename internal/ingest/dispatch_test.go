package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

type stubLoader struct {
	name  string
	score int
}

func (s stubLoader) Name() string        { return s.name }
func (s stubLoader) Detect(LoadHint) int { return s.score }

func TestRegistryPickHighestScoreWins(t *testing.T) {
	r := NewRegistry()
	r.Register(stubLoader{"low", 10})
	r.Register(stubLoader{"high", 90})
	r.Register(stubLoader{"mid", 50})
	got := r.Pick(LoadHint{})
	if got == nil || got.Name() != "high" {
		t.Fatalf("expected 'high', got %v", got)
	}
}

func TestRegistryTieBrokenAlphabetically(t *testing.T) {
	r := NewRegistry()
	r.Register(stubLoader{"zeta", 50})
	r.Register(stubLoader{"alpha", 50})
	r.Register(stubLoader{"mike", 50})
	got := r.Pick(LoadHint{})
	if got == nil || got.Name() != "alpha" {
		t.Fatalf("expected 'alpha' (alphabetic tie-break), got %v", got)
	}
}

func TestRegistryNoMatchReturnsNil(t *testing.T) {
	r := NewRegistry()
	r.Register(stubLoader{"zero", 0})
	if got := r.Pick(LoadHint{}); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestHintForFileReadsSniffAndMTime(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sample.log")
	body := []byte("2026-05-16 10:00:00.000 [INF] hello\n")
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := HintForFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if h.Ext != ".log" {
		t.Errorf("ext=%q, want .log", h.Ext)
	}
	if string(h.Sniff) != string(body) {
		t.Errorf("sniff mismatch")
	}
	if h.MTime.IsZero() {
		t.Errorf("mtime zero")
	}
}

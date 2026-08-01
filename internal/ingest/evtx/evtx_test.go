package evtx

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/labmk/obs-viewer/internal/ingest"
)

// sampleEvtxPath points at the committed fixture, resolved relative to
// this source file so the test works from any working directory and on
// any machine. EVTX is a binary format that test-fixtures/generate.py
// cannot synthesize, so the fixture is optional — tests needing a real
// file skip themselves when it is absent and CI without it still
// passes. See REQUIREMENTS.md (REQ-DT-02) for how to supply one.
var sampleEvtxPath = func() string {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	// internal/ingest/evtx/evtx_test.go -> repo root
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(self))))
	return filepath.Join(root, "test-fixtures", "formats", "sample.evtx")
}()

func TestDetectsEvtxMagic(t *testing.T) {
	l := New()
	got := l.Detect(ingest.LoadHint{Sniff: []byte("ElfFile\x00rest...")})
	if got != 100 {
		t.Errorf("got %d, want 100", got)
	}
}

func TestRejectsNonEvtx(t *testing.T) {
	l := New()
	cases := [][]byte{
		[]byte(""),
		[]byte("ElfFile"),     // missing trailing \0
		[]byte("ElfFileX..."), // 8th byte not \0
		[]byte("not evtx at all"),
	}
	for i, c := range cases {
		if got := l.Detect(ingest.LoadHint{Sniff: c}); got != 0 {
			t.Errorf("case %d: got %d, want 0", i, got)
		}
	}
}

func TestStreamRealSample(t *testing.T) {
	if _, err := os.Stat(sampleEvtxPath); err != nil {
		t.Skipf("sample EVTX not present: %s", sampleEvtxPath)
	}
	h, err := ingest.HintForFile(sampleEvtxPath)
	if err != nil {
		t.Fatal(err)
	}
	l := New()
	if l.Detect(h) != 100 {
		t.Fatal("Detect did not score 100 on real EVTX")
	}

	var first ingest.Record
	count := 0
	err = l.Stream(context.Background(), h, func(r ingest.Record) error {
		if count == 0 {
			first = r
		}
		count++
		if count >= 50 {
			// don't drain a large file in the test; the first
			// 50 events prove the streaming + flatten path works
			return context.Canceled
		}
		return nil
	})
	if err != nil && err != context.Canceled {
		t.Fatalf("Stream: %v", err)
	}
	if count == 0 {
		t.Fatal("emitted no records")
	}
	if _, ok := first[ingest.FieldTimestamp].(string); !ok {
		t.Errorf("@timestamp missing or wrong type: %T %v", first[ingest.FieldTimestamp], first[ingest.FieldTimestamp])
	}
	if first[ingest.FieldSource] != "evtx" {
		t.Errorf("_source_format=%v", first[ingest.FieldSource])
	}
	// Every record must have a Channel under Event.System
	if _, ok := first["Event.System.Channel"]; !ok {
		t.Errorf("Event.System.Channel missing; keys: %v", keysOf(first))
	}
}

func keysOf(r ingest.Record) []string {
	out := make([]string, 0, len(r))
	for k := range r {
		out = append(out, k)
	}
	return out
}

package evtx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/labmk/kopusha/internal/ingest"
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

// The parser reports SystemTime as a float64 of Unix seconds, not as a
// string — converting it is this adapter's job, and getting it wrong
// silently produces rows that sort into 1970 rather than failing.
func TestEventTimestampConvertsUnixFloat(t *testing.T) {
	fallback := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	rec := ingest.Record{systemTimePath: 1549731924.6727583}

	got := eventTimestamp(rec, fallback)
	want := "2019-02-09T17:05:24"
	if len(got) < len(want) || got[:len(want)] != want {
		t.Errorf("got %q, want it to start with %q", got, want)
	}
	if !strings.HasSuffix(got, "Z") {
		t.Errorf("got %q, want a UTC (Z-suffixed) timestamp", got)
	}
}

func TestEventTimestampFallsBackToMTime(t *testing.T) {
	fallback := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)
	cases := map[string]ingest.Record{
		"missing":     {},
		"unparseable": {systemTimePath: "not a number"},
		"zero":        {systemTimePath: float64(0)},
		"negative":    {systemTimePath: float64(-1)},
	}
	for name, rec := range cases {
		t.Run(name, func(t *testing.T) {
			if got := eventTimestamp(rec, fallback); got != "2020-03-04T05:06:07Z" {
				t.Errorf("got %q, want the mtime fallback", got)
			}
		})
	}
}

// A record with no @timestamp at all would break the engine's union
// query, so the contract is that every record carries one.
func TestStreamAlwaysSetsTimestamp(t *testing.T) {
	if _, err := os.Stat(sampleEvtxPath); err != nil {
		t.Skipf("sample EVTX not present: %s", sampleEvtxPath)
	}
	h, err := ingest.HintForFile(sampleEvtxPath)
	if err != nil {
		t.Fatal(err)
	}
	err = New().Stream(context.Background(), h, func(r ingest.Record) error {
		ts, ok := r[ingest.FieldTimestamp].(string)
		if !ok || ts == "" {
			return fmt.Errorf("record without @timestamp: %v", keysOf(r))
		}
		if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
			return fmt.Errorf("@timestamp %q is not RFC3339Nano: %w", ts, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Detect must not be fooled by a file that merely mentions the magic.
func TestStreamRejectsNonEvtxFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "notevtx.bin")
	if err := os.WriteFile(p, []byte("ElfFile\x00 but truncated nonsense"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := ingest.HintForFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// The point is that it returns an error rather than panicking, which
	// is what the previous parser did from inside its own goroutine.
	err = New().Stream(context.Background(), h, func(ingest.Record) error { return nil })
	if err == nil {
		t.Error("want an error for a truncated EVTX, got nil")
	}
}

func keysOf(r ingest.Record) []string {
	out := make([]string, 0, len(r))
	for k := range r {
		out = append(out, k)
	}
	return out
}

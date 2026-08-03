package parquet

import (
	"strings"
	"testing"

	"github.com/labmk/kopusha/internal/ingest"
)

func TestDetectMagicBytes(t *testing.T) {
	l := New()
	if got := l.Detect(ingest.LoadHint{Sniff: []byte("PAR1\x15\x04\x15")}); got != 100 {
		t.Errorf("magic bytes scored %d, want 100", got)
	}
}

func TestDetectExtensionWithoutMagic(t *testing.T) {
	l := New()
	for _, ext := range []string{".parquet", ".pq"} {
		if got := l.Detect(ingest.LoadHint{Ext: ext}); got != 90 {
			t.Errorf("%s scored %d, want 90", ext, got)
		}
	}
}

func TestDetectRejectsOtherFormats(t *testing.T) {
	l := New()
	cases := map[string]ingest.LoadHint{
		"ndjson":          {Sniff: []byte(`{"a":1}`), Ext: ".ndjson"},
		"evtx":            {Sniff: []byte("ElfFile\x00")},
		"xml":             {Sniff: []byte("<Events>")},
		"plain text":      {Sniff: []byte("2026-01-01 INFO hello"), Ext: ".log"},
		"empty":           {},
		"truncated magic": {Sniff: []byte("PAR")},
		// PARE marks an encrypted Parquet file. DuckDB cannot read it
		// without a key, so claiming it would turn a clear "unsupported"
		// into a confusing load failure.
		"encrypted": {Sniff: []byte("PARE\x15\x04")},
	}
	for name, h := range cases {
		if got := l.Detect(h); got != 0 {
			t.Errorf("%s scored %d, want 0", name, got)
		}
	}
}

// The magic bytes must outrank the extension, so a file named .log that
// is really Parquet still routes correctly.
func TestMagicBeatsExtension(t *testing.T) {
	l := New()
	got := l.Detect(ingest.LoadHint{Sniff: []byte("PAR1"), Ext: ".log"})
	if got != 100 {
		t.Errorf("got %d, want 100 — content must outrank the name", got)
	}
}

func TestUsesDirectPath(t *testing.T) {
	if !New().UseDirectPath() {
		t.Error("Parquet must use the direct path; streaming it through Go " +
			"would discard the types the format exists to preserve")
	}
}

// The engine hands ReadExpr an already-escaped path. A quote in a
// filename must survive as the doubled form rather than terminating the
// SQL string.
func TestReadExprComposesEscapedPath(t *testing.T) {
	got := New().ReadExpr("/tmp/it''s.parquet")
	want := "read_parquet('/tmp/it''s.parquet')"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if strings.Count(got, "'")%2 != 0 {
		t.Errorf("unbalanced quotes in %q", got)
	}
}

func TestName(t *testing.T) {
	if New().Name() != "parquet" {
		t.Errorf("got %q", New().Name())
	}
}

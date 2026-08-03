package ndjson

import (
	"testing"

	"github.com/labmk/kopusha/internal/ingest"
)

func TestDetectExplicitNdjsonExt(t *testing.T) {
	l := New()
	got := l.Detect(ingest.LoadHint{Ext: ".ndjson", Sniff: []byte("anything")})
	if got != 90 {
		t.Errorf("got %d, want 90", got)
	}
}

func TestDetectJsonObjectFirstLine(t *testing.T) {
	l := New()
	body := []byte(`{"a":1,"b":"x"}` + "\n" + `{"a":2,"b":"y"}`)
	got := l.Detect(ingest.LoadHint{Ext: ".log", Sniff: body})
	if got != 90 {
		t.Errorf("got %d, want 90 for valid first-line JSON", got)
	}
}

func TestDetectJsonExtensionDemoted(t *testing.T) {
	l := New()
	body := []byte(`{"a":1}` + "\n")
	got := l.Detect(ingest.LoadHint{Ext: ".json", Sniff: body})
	if got != 70 {
		t.Errorf("got %d, want 70 for .json extension", got)
	}
}

func TestDetectArrayRejected(t *testing.T) {
	l := New()
	body := []byte(`[{"a":1},{"a":2}]`)
	got := l.Detect(ingest.LoadHint{Ext: ".json", Sniff: body})
	if got != 0 {
		t.Errorf("got %d, want 0 for top-level array", got)
	}
}

func TestDetectGarbageRejected(t *testing.T) {
	l := New()
	body := []byte("{not actually json")
	got := l.Detect(ingest.LoadHint{Ext: ".log", Sniff: body})
	if got != 0 {
		t.Errorf("got %d, want 0 for malformed JSON-looking text", got)
	}
}

func TestDirectIngester(t *testing.T) {
	if !New().UseDirectPath() {
		t.Fatal("NDJSON loader must be a direct ingester")
	}
}

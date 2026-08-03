package ingest_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/labmk/kopusha/internal/ingest"
	"github.com/labmk/kopusha/internal/ingest/block"
	"github.com/labmk/kopusha/internal/ingest/evtx"
	"github.com/labmk/kopusha/internal/ingest/line"
	"github.com/labmk/kopusha/internal/ingest/ndjson"
	"github.com/labmk/kopusha/internal/ingest/parquet"
	ingestxml "github.com/labmk/kopusha/internal/ingest/xml"
)

// This test is the executable half of the REQ-DT matrix in
// REQUIREMENTS.md: it wires up the same registry main.go builds, points
// it at every committed fixture, and asserts the dispatcher routes each
// one to the intended adapter and emits the documented
// `_source_format`.
//
// It exists to catch the failure mode where a parsers.d/ regex is
// edited and a fixture silently stops matching — the file still loads
// (a lower-priority rule or a fallback picks it up), rows still appear,
// and only the `_source_format` value reveals the mis-route.
//
// Regenerate fixtures with `python3 test-fixtures/generate.py` after
// changing a rule.

// repoRoot walks up from this source file to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/ingest/fixtures_test.go -> ../../
	return filepath.Dir(filepath.Dir(filepath.Dir(self)))
}

func testRegistry(t *testing.T) *ingest.Registry {
	t.Helper()
	root := repoRoot(t)
	rules, err := ingest.LoadRules(filepath.Join(root, "parsers.d"))
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	blockLoader, err := block.New(rules.Block)
	if err != nil {
		t.Fatalf("block.New: %v", err)
	}
	lineLoader, err := line.New(rules.Line)
	if err != nil {
		t.Fatalf("line.New: %v", err)
	}
	xmlLoader, err := ingestxml.New(rules.XML)
	if err != nil {
		t.Fatalf("xml.New: %v", err)
	}
	reg := ingest.NewRegistry()
	reg.Register(ndjson.New())
	reg.Register(parquet.New())
	reg.Register(evtx.New())
	reg.Register(blockLoader)
	reg.Register(lineLoader)
	reg.Register(xmlLoader)
	return reg
}

func TestFixturesRouteToExpectedAdapter(t *testing.T) {
	cases := []struct {
		req      string // REQUIREMENTS.md row
		fixture  string
		loader   string // expected Loader.Name()
		format   string // expected _source_format; "" for the NDJSON direct path
		direct   bool
		optional bool // fixture may be absent (see REQ-DT-02)
	}{
		{"REQ-DT-01", "sample.ndjson", "ndjson", "", true, false},
		{"REQ-DT-10", "sample.parquet", "parquet", "", true, false},
		{"REQ-DT-02", "sample.evtx", "evtx", "evtx", false, true},
		{"REQ-DT-03", "xml-row-element.txt", "xml", "xml:Event", false, false},
		{"REQ-DT-04", "block-keyvalue-dash.txt", "block", "block:keyvalue-dash-separated", false, false},
		{"REQ-DT-05", "line-iso-bracket.log", "line", "line:iso-bracket", false, false},
		{"REQ-DT-06", "line-dashdate-level.log", "line", "line:dashdate-level", false, false},
		{"REQ-DT-07", "line-dotdate-pidtid.log", "line", "line:dotdate-pidtid", false, false},
		{"REQ-DT-08", "line-time-pidtid.log", "line", "line:time-pidtid", false, false},
		{"REQ-DT-09", "line-time-dotdate.log", "line", "line:time-dotdate", false, false},
	}

	reg := testRegistry(t)
	dir := filepath.Join(repoRoot(t), "test-fixtures", "formats")

	for _, tc := range cases {
		t.Run(tc.req+" "+tc.fixture, func(t *testing.T) {
			path := filepath.Join(dir, tc.fixture)
			if _, err := os.Stat(path); err != nil {
				if tc.optional {
					t.Skipf("optional fixture %s not present: %v", tc.fixture, err)
				}
				t.Fatalf("fixture missing: %v", err)
			}

			hint, err := ingest.HintForFile(path)
			if err != nil {
				t.Fatalf("HintForFile: %v", err)
			}
			got := reg.Pick(hint)
			if got == nil {
				t.Fatal("no loader matched")
			}
			if got.Name() != tc.loader {
				t.Fatalf("routed to %q, want %q", got.Name(), tc.loader)
			}

			if tc.direct {
				di, ok := got.(ingest.DirectIngester)
				if !ok || !di.UseDirectPath() {
					t.Fatal("expected the direct-path ingester")
				}
				return
			}

			streamer, ok := got.(ingest.RecordStreamer)
			if !ok {
				t.Fatalf("%s does not implement RecordStreamer", got.Name())
			}

			var first ingest.Record
			var n int
			err = streamer.Stream(context.Background(), hint, func(r ingest.Record) error {
				if n == 0 {
					first = r
				}
				n++
				return nil
			})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if n == 0 {
				t.Fatal("no records emitted")
			}

			if sf, _ := first["_source_format"].(string); sf != tc.format {
				t.Errorf("_source_format = %q, want %q", sf, tc.format)
			}
			// Contract from REQUIREMENTS.md: every non-NDJSON record
			// carries an ISO-8601 UTC @timestamp.
			ts, _ := first["@timestamp"].(string)
			if ts == "" {
				t.Error("@timestamp missing or not a string")
			}
			t.Logf("%s: %d records, first @timestamp=%s", tc.fixture, n, ts)
		})
	}
}

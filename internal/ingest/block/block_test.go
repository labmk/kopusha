package block

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labmk/obs-viewer/internal/ingest"
)

// Default rule used in tests — same shape as the YAML we ship.
func defaultRawRule() ingest.RawRule {
	return ingest.RawRule{
		Source: "test",
		Family: "block",
		Name:   "keyvalue-dash-separated",
		Data: map[string]any{
			"family":     "block",
			"name":       "keyvalue-dash-separated",
			"priority":   70,
			"separator":  `^-{20,}$`,
			"field":      `^(?P<key>[A-Za-z][\w ]*?):[ \t]?(?P<value>.*)$`,
			"ts_field":   []any{"Timestamp", "@timestamp"},
			"ts_layouts": []any{time.RFC3339Nano, "2006-01-02T15:04:05Z07:00"},
		},
	}
}

func loaderForTest(t *testing.T) *Loader {
	t.Helper()
	l, err := New([]ingest.RawRule{defaultRawRule()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDetectMatchesDashSeparator(t *testing.T) {
	l := loaderForTest(t)
	got := l.Detect(ingest.LoadHint{Sniff: []byte("----------------------------------------\nMessage: hi\n")})
	if got != 70 {
		t.Errorf("got %d, want 70", got)
	}
}

func TestDetectRejectsRandomText(t *testing.T) {
	l := loaderForTest(t)
	got := l.Detect(ingest.LoadHint{Sniff: []byte("just some lines\nno separator here\n")})
	if got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestStreamEmitsOneRecordPerBlock(t *testing.T) {
	body := `----------------------------------------
Message: First
Severity: Information
Timestamp: 2026-03-25T04:00:54.093+01:00

----------------------------------------
Message: Second
Severity: Warning
Timestamp: 2026-03-25T04:00:55.500+01:00

----------------------------------------
`
	l := loaderForTest(t)
	path := writeTemp(t, body)

	var got []ingest.Record
	err := l.Stream(context.Background(), ingest.LoadHint{Path: path}, func(r ingest.Record) error {
		got = append(got, r)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("emitted %d records, want 2", len(got))
	}
	if got[0]["Message"] != "First" {
		t.Errorf("record 0 Message=%q", got[0]["Message"])
	}
	if got[1]["Severity"] != "Warning" {
		t.Errorf("record 1 Severity=%q", got[1]["Severity"])
	}
}

func TestStreamCoalescesMultiLineValues(t *testing.T) {
	body := `----------------------------------------
Message: Starting WorkflowServer
- 97 ms startup delay
some more continuation
Severity: Information
Timestamp: 2026-03-25T04:00:54.093+01:00
----------------------------------------
`
	l := loaderForTest(t)
	path := writeTemp(t, body)

	var rec ingest.Record
	err := l.Stream(context.Background(), ingest.LoadHint{Path: path}, func(r ingest.Record) error {
		rec = r
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wantMsg := "Starting WorkflowServer\n- 97 ms startup delay\nsome more continuation"
	if rec["Message"] != wantMsg {
		t.Errorf("Message=%q\n  want %q", rec["Message"], wantMsg)
	}
	if rec["Severity"] != "Information" {
		t.Errorf("Severity=%q, want Information (continuation shouldn't bleed into next key)", rec["Severity"])
	}
}

func TestStreamNormalizesTimestampToUTC(t *testing.T) {
	body := `----------------------------------------
Message: x
Timestamp: 2026-03-25T04:00:54.093+01:00
----------------------------------------
`
	l := loaderForTest(t)
	path := writeTemp(t, body)

	var rec ingest.Record
	_ = l.Stream(context.Background(), ingest.LoadHint{Path: path}, func(r ingest.Record) error {
		rec = r
		return nil
	})
	ts, ok := rec[ingest.FieldTimestamp].(string)
	if !ok {
		t.Fatalf("@timestamp missing, got record %+v", rec)
	}
	// +01:00 source → UTC: 04:00:54.093+01:00 = 03:00:54.093Z
	if ts != "2026-03-25T03:00:54.093Z" {
		t.Errorf("@timestamp=%q, want 2026-03-25T03:00:54.093Z", ts)
	}
}

func TestStreamSetsSourceFormat(t *testing.T) {
	body := `----------------------------------------
Message: x
Timestamp: 2026-03-25T04:00:54.093+01:00
----------------------------------------
`
	l := loaderForTest(t)
	path := writeTemp(t, body)

	var rec ingest.Record
	_ = l.Stream(context.Background(), ingest.LoadHint{Path: path}, func(r ingest.Record) error {
		rec = r
		return nil
	})
	if rec[ingest.FieldSource] != "block:keyvalue-dash-separated" {
		t.Errorf("_source_format=%v", rec[ingest.FieldSource])
	}
}

func TestStreamIgnoresPreamble(t *testing.T) {
	body := `some random preamble
that has no separator yet
----------------------------------------
Message: real
Timestamp: 2026-03-25T04:00:54.093+01:00
----------------------------------------
`
	l := loaderForTest(t)
	path := writeTemp(t, body)

	var n int
	_ = l.Stream(context.Background(), ingest.LoadHint{Path: path}, func(r ingest.Record) error {
		n++
		return nil
	})
	if n != 1 {
		t.Errorf("emitted %d records, want 1 (preamble must be ignored)", n)
	}
}

func TestStreamSkipsEmptyRecords(t *testing.T) {
	body := `----------------------------------------
----------------------------------------
Message: real
Timestamp: 2026-03-25T04:00:54.093+01:00
----------------------------------------
`
	l := loaderForTest(t)
	path := writeTemp(t, body)

	var n int
	_ = l.Stream(context.Background(), ingest.LoadHint{Path: path}, func(r ingest.Record) error {
		n++
		return nil
	})
	if n != 1 {
		t.Errorf("emitted %d records, want 1 (consecutive separators must not produce empty rows)", n)
	}
}

func TestCompileRuleRejectsMissingRequired(t *testing.T) {
	_, err := New([]ingest.RawRule{{Data: map[string]any{"separator": "----"}}})
	if err == nil {
		t.Error("expected error: missing field regex")
	}
	_, err = New([]ingest.RawRule{{Data: map[string]any{"field": `^(?P<key>\w+):(?P<value>.*)$`}}})
	if err == nil {
		t.Error("expected error: missing separator regex")
	}
}

func TestCompileRuleRejectsFieldRegexWithoutNamedGroups(t *testing.T) {
	_, err := New([]ingest.RawRule{{Data: map[string]any{
		"separator": "----",
		"field":     `^(\w+):(.*)$`,
	}}})
	if err == nil {
		t.Error("expected error: field regex needs named groups")
	}
}

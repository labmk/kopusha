package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Pure helpers: canonicalColumns + buildUnionSelect ---

func newWithTables(tableCols map[string][]string, fileOrder []string) *Engine {
	e := &Engine{
		files:          make(map[string]*FileInfo),
		tableCols:      tableCols,
		pathIndex:      make(map[string]string),
		timestampField: "@timestamp",
	}
	for _, t := range fileOrder {
		id := strings.TrimPrefix(t, "file_")
		e.files[id] = &FileInfo{ID: id, TableName: t, Enabled: true}
		e.fileOrder = append(e.fileOrder, id)
	}
	return e
}

func TestCanonicalColumnsFirstCasingWins(t *testing.T) {
	e := newWithTables(map[string][]string{
		"file_a": {"Message", "Severity", "Timestamp"},
		"file_b": {"message", "severity", "Timestamp", "Extra"},
	}, []string{"file_a", "file_b"})

	order, byLower := e.canonicalColumns([]string{"file_a", "file_b"})

	want := []string{"Message", "Severity", "Timestamp", "Extra"}
	if len(order) != len(want) {
		t.Fatalf("order len=%d, want %d: %v", len(order), len(want), order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q", i, order[i], want[i])
		}
	}
	if byLower["message"] != "Message" {
		t.Errorf("byLower[message] = %q, want Message", byLower["message"])
	}
	if byLower["extra"] != "Extra" {
		t.Errorf("byLower[extra] = %q, want Extra", byLower["extra"])
	}
}

func TestCanonicalColumnsSecondFileCasingWins(t *testing.T) {
	// Iteration order matters: if the lowercase file comes first, that's
	// the canonical.
	e := newWithTables(map[string][]string{
		"file_a": {"message"},
		"file_b": {"Message"},
	}, []string{"file_a", "file_b"})

	order, byLower := e.canonicalColumns([]string{"file_a", "file_b"})
	if len(order) != 1 || order[0] != "message" {
		t.Errorf("order = %v, want [message]", order)
	}
	if byLower["message"] != "message" {
		t.Errorf("byLower[message] = %q, want message", byLower["message"])
	}
}

func TestBuildUnionSelectAliasesToCanonical(t *testing.T) {
	e := newWithTables(map[string][]string{
		"file_a": {"Message", "Severity"},
		"file_b": {"message", "severity", "Extra"},
	}, []string{"file_a", "file_b"})

	order, byLower := e.canonicalColumns([]string{"file_a", "file_b"})

	selB := e.buildUnionSelect("file_b", order, byLower, false)
	// file_b has lowercase columns; the SELECT must project them under
	// the canonical (file_a's casing) names.
	if !strings.Contains(selB, `CAST("message" AS VARCHAR) AS "Message"`) {
		t.Errorf("expected message→Message alias, got: %s", selB)
	}
	if !strings.Contains(selB, `CAST("severity" AS VARCHAR) AS "Severity"`) {
		t.Errorf("expected severity→Severity alias, got: %s", selB)
	}
	if !strings.Contains(selB, `CAST("Extra" AS VARCHAR) AS "Extra"`) {
		t.Errorf("expected Extra→Extra alias, got: %s", selB)
	}
}

func TestBuildUnionSelectFillsMissingWithNull(t *testing.T) {
	e := newWithTables(map[string][]string{
		"file_a": {"Message", "Severity"},
		"file_b": {"Message"},
	}, []string{"file_a", "file_b"})

	order, byLower := e.canonicalColumns([]string{"file_a", "file_b"})
	selB := e.buildUnionSelect("file_b", order, byLower, false)
	if !strings.Contains(selB, `CAST(NULL AS VARCHAR) AS "Severity"`) {
		t.Errorf("expected NULL fill for Severity, got: %s", selB)
	}
}

func TestBuildUnionSelectCoalescesIntraTableCollisions(t *testing.T) {
	// Pathological but possible: a single file has both "Message" and
	// "message" columns (e.g. an NDJSON file where two records use
	// different casings). They must merge into the canonical projection
	// without dropping data.
	e := newWithTables(map[string][]string{
		"file_a": {"Message", "message", "Severity"},
	}, []string{"file_a"})

	order, byLower := e.canonicalColumns([]string{"file_a"})
	sel := e.buildUnionSelect("file_a", order, byLower, false)
	if !strings.Contains(sel, `COALESCE(CAST("Message" AS VARCHAR), CAST("message" AS VARCHAR)) AS "Message"`) {
		t.Errorf("expected COALESCE merge, got: %s", sel)
	}
}

// --- Integration: actual DuckDB engine, two case-different NDJSON files ---

func TestEndToEndMergesDifferentlyCasedFields(t *testing.T) {
	dir := t.TempDir()
	// File A: capital-M Message
	pathA := filepath.Join(dir, "a.ndjson")
	mustWriteJSON(t, pathA,
		map[string]any{"@timestamp": "2026-05-17T10:00:00Z", "Message": "alpha-1", "Severity": "INFO"},
		map[string]any{"@timestamp": "2026-05-17T10:00:01Z", "Message": "alpha-2", "Severity": "WARN"},
	)
	// File B: lowercase message
	pathB := filepath.Join(dir, "b.ndjson")
	mustWriteJSON(t, pathB,
		map[string]any{"@timestamp": "2026-05-17T10:00:02Z", "message": "beta-1", "level": "INFO"},
		map[string]any{"@timestamp": "2026-05-17T10:00:03Z", "message": "beta-2", "level": "ERROR"},
	)

	eng, err := New()
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()
	if err := eng.LoadFile(pathA); err != nil {
		t.Fatalf("LoadFile A: %v", err)
	}
	if err := eng.LoadFile(pathB); err != nil {
		t.Fatalf("LoadFile B: %v", err)
	}

	fields, err := eng.GetFields()
	if err != nil {
		t.Fatalf("GetFields: %v", err)
	}
	// Expect a SINGLE "Message" column (first-encountered casing), not
	// both "Message" and "message".
	gotMessage := 0
	for _, f := range fields {
		if strings.EqualFold(f, "Message") {
			if f != "Message" {
				t.Errorf("Message canonical casing wrong: %q", f)
			}
			gotMessage++
		}
	}
	if gotMessage != 1 {
		t.Errorf("expected exactly 1 Message-family field, got %d (fields=%v)", gotMessage, fields)
	}

	// Query: confirm both files' rows land in the same Message column
	// when sorted by @timestamp.
	res, err := eng.Query(QueryRequest{Limit: 10, SortOrder: "asc"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.TotalCount != 4 {
		t.Errorf("total=%d, want 4", res.TotalCount)
	}
	var msgs []string
	for _, r := range res.Rows {
		if m, ok := r["Message"].(string); ok {
			msgs = append(msgs, m)
		}
	}
	wantMsgs := []string{"alpha-1", "alpha-2", "beta-1", "beta-2"}
	if len(msgs) != len(wantMsgs) {
		t.Fatalf("merged Message values: got %v, want %v", msgs, wantMsgs)
	}
	for i := range wantMsgs {
		if msgs[i] != wantMsgs[i] {
			t.Errorf("msgs[%d] = %q, want %q", i, msgs[i], wantMsgs[i])
		}
	}
}

// TestQuoteIdentResistsBreakout — load an NDJSON whose key contains a
// double quote (legal JSON, dangerous if interpolated raw into a SQL
// identifier). Before quoteIdent doubled the embedded ", the SELECT
// generated for this column was broken SQL and the Query failed; with
// the fix, the column round-trips and queries normally.
func TestQuoteIdentResistsBreakout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hostile.ndjson")
	// Key contains a literal " which, if not doubled, would close the
	// surrounding identifier quotes mid-SELECT.
	mustWriteJSON(t, path,
		map[string]any{"@timestamp": "2026-05-18T10:00:00Z", `bad"name`: "v1"},
		map[string]any{"@timestamp": "2026-05-18T10:00:01Z", `bad"name`: "v2"},
	)

	eng, err := New()
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()
	if err := eng.LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	fields, err := eng.GetFields()
	if err != nil {
		t.Fatalf("GetFields: %v", err)
	}
	found := false
	for _, f := range fields {
		if f == `bad"name` {
			found = true
		}
	}
	if !found {
		t.Errorf(`field 'bad"name' missing; got %v`, fields)
	}

	res, err := eng.Query(QueryRequest{Limit: 10, SortOrder: "asc"})
	if err != nil {
		t.Fatalf("Query failed (likely SQL breakout): %v", err)
	}
	if res.TotalCount != 2 {
		t.Errorf("total=%d, want 2", res.TotalCount)
	}
	if len(res.Rows) != 2 || res.Rows[0][`bad"name`] != "v1" || res.Rows[1][`bad"name`] != "v2" {
		t.Errorf("row values wrong: %+v", res.Rows)
	}

	// Filtering on the hostile field name must also work (buildFilterCondition path).
	res, err = eng.Query(QueryRequest{
		Limit:   10,
		Filters: []Filter{{Field: `bad"name`, Operator: "is", Value: "v1"}},
	})
	if err != nil {
		t.Fatalf("filtered Query failed: %v", err)
	}
	if res.TotalCount != 1 {
		t.Errorf("filtered total=%d, want 1", res.TotalCount)
	}
}

func mustWriteJSON(t *testing.T, path string, recs ...map[string]any) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
}

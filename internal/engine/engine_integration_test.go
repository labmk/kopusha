package engine

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Integration tests against a real in-memory DuckDB. Exercise the
// operators end-to-end (the SQL string emitted by builders may compile
// without raising an error and still mean something subtly different
// from what we intended — these tests catch that).

func loadOne(t *testing.T, recs ...map[string]any) *Engine {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.ndjson")
	mustWriteJSON(t, path, recs...)
	eng, err := New()
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if err := eng.LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	return eng
}

func TestQueryEmptyEngineReturnsEmpty(t *testing.T) {
	eng, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer eng.Close()
	res, err := eng.Query(QueryRequest{Limit: 10})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.TotalCount != 0 || len(res.Rows) != 0 {
		t.Errorf("expected empty result, got total=%d rows=%d", res.TotalCount, len(res.Rows))
	}
	if len(res.Fields) != 0 {
		t.Errorf("expected empty fields, got %v", res.Fields)
	}
}

func TestQueryOperatorIs(t *testing.T) {
	eng := loadOne(t,
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "level": "INFO", "msg": "a"},
		map[string]any{"@timestamp": "2026-05-20T10:00:01Z", "level": "ERROR", "msg": "b"},
		map[string]any{"@timestamp": "2026-05-20T10:00:02Z", "level": "WARN", "msg": "c"},
	)
	res, err := eng.Query(QueryRequest{
		Limit:   10,
		Filters: []Filter{{Field: "level", Operator: "is", Value: "error"}}, // case-insensitive
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.TotalCount != 1 {
		t.Errorf("count=%d, want 1", res.TotalCount)
	}
	if len(res.Rows) != 1 || res.Rows[0]["msg"] != "b" {
		t.Errorf("expected msg=b, got %+v", res.Rows)
	}
}

func TestQueryOperatorContains(t *testing.T) {
	eng := loadOne(t,
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "msg": "hello world"},
		map[string]any{"@timestamp": "2026-05-20T10:00:01Z", "msg": "WORLDLY problem"},
		map[string]any{"@timestamp": "2026-05-20T10:00:02Z", "msg": "no match"},
	)
	res, err := eng.Query(QueryRequest{
		Limit:   10,
		Filters: []Filter{{Field: "msg", Operator: "contains", Value: "world"}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.TotalCount != 2 {
		t.Errorf("count=%d, want 2 (case-insensitive)", res.TotalCount)
	}
}

func TestQueryOperatorWildcard(t *testing.T) {
	eng := loadOne(t,
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "host": "host-001.eu"},
		map[string]any{"@timestamp": "2026-05-20T10:00:01Z", "host": "host-002.eu"},
		map[string]any{"@timestamp": "2026-05-20T10:00:02Z", "host": "host-001.us"},
	)
	res, err := eng.Query(QueryRequest{
		Limit:   10,
		Filters: []Filter{{Field: "host", Operator: "wildcard", Value: "host-*.eu"}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.TotalCount != 2 {
		t.Errorf("count=%d, want 2", res.TotalCount)
	}
}

func TestQueryOperatorExistsAndDoesNotExist(t *testing.T) {
	eng := loadOne(t,
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "user": "alice"},
		map[string]any{"@timestamp": "2026-05-20T10:00:01Z", "user": ""},
		map[string]any{"@timestamp": "2026-05-20T10:00:02Z"}, // user missing
	)
	resHas, err := eng.Query(QueryRequest{
		Limit:   10,
		Filters: []Filter{{Field: "user", Operator: "exists"}},
	})
	if err != nil {
		t.Fatalf("Query exists: %v", err)
	}
	if resHas.TotalCount != 1 {
		t.Errorf("exists count=%d, want 1 (empty string excluded)", resHas.TotalCount)
	}
	resAbsent, err := eng.Query(QueryRequest{
		Limit:   10,
		Filters: []Filter{{Field: "user", Operator: "does_not_exist"}},
	})
	if err != nil {
		t.Fatalf("Query does_not_exist: %v", err)
	}
	if resAbsent.TotalCount != 2 {
		t.Errorf("does_not_exist count=%d, want 2 (empty + missing)", resAbsent.TotalCount)
	}
}

func TestQueryOperatorIsOneOf(t *testing.T) {
	eng := loadOne(t,
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "level": "INFO"},
		map[string]any{"@timestamp": "2026-05-20T10:00:01Z", "level": "WARN"},
		map[string]any{"@timestamp": "2026-05-20T10:00:02Z", "level": "ERROR"},
		map[string]any{"@timestamp": "2026-05-20T10:00:03Z", "level": "DEBUG"},
	)
	// is_one_of: comma list, case-insensitive.
	res, err := eng.Query(QueryRequest{
		Limit:   10,
		Filters: []Filter{{Field: "level", Operator: "is_one_of", Value: "warn, error"}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.TotalCount != 2 {
		t.Errorf("count=%d, want 2", res.TotalCount)
	}

	// is_not_one_of: NULL-keeps behaviour. Add a record with no level.
	eng2 := loadOne(t,
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "level": "INFO"},
		map[string]any{"@timestamp": "2026-05-20T10:00:01Z", "level": "WARN"},
		map[string]any{"@timestamp": "2026-05-20T10:00:02Z"}, // no level
	)
	res, err = eng2.Query(QueryRequest{
		Limit:   10,
		Filters: []Filter{{Field: "level", Operator: "is_not_one_of", Value: "warn"}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// INFO + NULL → 2
	if res.TotalCount != 2 {
		t.Errorf("count=%d, want 2 (INFO + NULL)", res.TotalCount)
	}
}

func TestQueryTimeRangeFilter(t *testing.T) {
	// The shape the frontend's TimeFilter sends. Bounds are compared as
	// timestamps rather than as text, so this is no longer the only
	// spelling that works — see
	// TestTimeFilterAcceptsEverySpellingOfAnInstant — but it is the one
	// the UI produces, so it stays covered on its own.
	eng := loadOne(t,
		map[string]any{"@timestamp": "2026-05-20T08:00:00Z", "n": 1},
		map[string]any{"@timestamp": "2026-05-20T09:00:00Z", "n": 2},
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "n": 3},
		map[string]any{"@timestamp": "2026-05-20T11:00:00Z", "n": 4},
	)
	from := "2026-05-20 09:00:00"
	to := "2026-05-20 10:00:00"
	res, err := eng.Query(QueryRequest{
		Limit:    10,
		TimeFrom: &from,
		TimeTo:   &to,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// Inclusive on both ends.
	if res.TotalCount != 2 {
		t.Errorf("count=%d, want 2 (inclusive)", res.TotalCount)
	}
}

func TestQueryPagination(t *testing.T) {
	var recs []map[string]any
	for i := 0; i < 20; i++ {
		recs = append(recs, map[string]any{
			"@timestamp": "2026-05-20T10:00:" + zeropad2(i) + "Z",
			"n":          i,
		})
	}
	eng := loadOne(t, recs...)
	// Limit + offset.
	page1, err := eng.Query(QueryRequest{Limit: 5, Offset: 0, SortOrder: "asc"})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if page1.TotalCount != 20 {
		t.Errorf("page1 total=%d, want 20", page1.TotalCount)
	}
	if len(page1.Rows) != 5 {
		t.Errorf("page1 rows=%d, want 5", len(page1.Rows))
	}
	page2, err := eng.Query(QueryRequest{Limit: 5, Offset: 5, SortOrder: "asc"})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2.Rows) != 5 {
		t.Errorf("page2 rows=%d, want 5", len(page2.Rows))
	}
	// Sequential @timestamps across pages, no overlap.
	last1 := page1.Rows[4]["@timestamp"]
	first2 := page2.Rows[0]["@timestamp"]
	if last1 == first2 {
		t.Errorf("pages overlap: last(page1)=%v first(page2)=%v", last1, first2)
	}
}

func TestQuerySortAscDesc(t *testing.T) {
	eng := loadOne(t,
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "n": 1},
		map[string]any{"@timestamp": "2026-05-20T10:00:02Z", "n": 3},
		map[string]any{"@timestamp": "2026-05-20T10:00:01Z", "n": 2},
	)
	asc, _ := eng.Query(QueryRequest{Limit: 10, SortOrder: "asc"})
	desc, _ := eng.Query(QueryRequest{Limit: 10, SortOrder: "desc"})
	if asc.Rows[0]["@timestamp"].(string) > asc.Rows[2]["@timestamp"].(string) {
		t.Errorf("asc not ascending: %v", asc.Rows)
	}
	if desc.Rows[0]["@timestamp"].(string) < desc.Rows[2]["@timestamp"].(string) {
		t.Errorf("desc not descending: %v", desc.Rows)
	}
}

func TestQueryFreeTextSearch(t *testing.T) {
	eng := loadOne(t,
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "msg": "needle in here", "host": "h1"},
		map[string]any{"@timestamp": "2026-05-20T10:00:01Z", "msg": "no", "host": "needle-host"},
		map[string]any{"@timestamp": "2026-05-20T10:00:02Z", "msg": "no", "host": "h3"},
	)
	res, err := eng.Query(QueryRequest{Limit: 10, SearchText: "needle"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// Both rows that contain "needle" in ANY column.
	if res.TotalCount != 2 {
		t.Errorf("count=%d, want 2", res.TotalCount)
	}
}

func TestStructSubPathFilter(t *testing.T) {
	// F56: filtering on a STRUCT sub-path like `nodeinfo.type` must
	// resolve through json_extract_string, not look for a literal-named
	// column.
	eng := loadOne(t,
		map[string]any{
			"@timestamp": "2026-05-20T10:00:00Z",
			"nodeinfo":   map[string]any{"type": "worker", "id": "n1"},
			"msg":        "a",
		},
		map[string]any{
			"@timestamp": "2026-05-20T10:00:01Z",
			"nodeinfo":   map[string]any{"type": "broker", "id": "n2"},
			"msg":        "b",
		},
		map[string]any{
			"@timestamp": "2026-05-20T10:00:02Z",
			"nodeinfo":   map[string]any{"type": "worker", "id": "n3"},
			"msg":        "c",
		},
	)

	fields, err := eng.GetFields()
	if err != nil {
		t.Fatalf("GetFields: %v", err)
	}
	if !containsStr(fields, "nodeinfo.type") {
		t.Errorf("expected nodeinfo.type in fields, got %v", fields)
	}

	res, err := eng.Query(QueryRequest{
		Limit:   10,
		Filters: []Filter{{Field: "nodeinfo.type", Operator: "is", Value: "worker"}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.TotalCount != 2 {
		t.Errorf("count=%d, want 2", res.TotalCount)
	}
}

func TestGetTimeRangeAcrossEnabledFiles(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.ndjson")
	b := filepath.Join(dir, "b.ndjson")
	mustWriteJSON(t, a,
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "x": 1},
		map[string]any{"@timestamp": "2026-05-20T12:00:00Z", "x": 2},
	)
	mustWriteJSON(t, b,
		map[string]any{"@timestamp": "2026-05-20T09:00:00Z", "x": 3},
		map[string]any{"@timestamp": "2026-05-20T13:00:00Z", "x": 4},
	)
	eng, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer eng.Close()
	if err := eng.LoadFile(a); err != nil {
		t.Fatalf("load a: %v", err)
	}
	if err := eng.LoadFile(b); err != nil {
		t.Fatalf("load b: %v", err)
	}
	tr, err := eng.GetTimeRange()
	if err != nil {
		t.Fatalf("GetTimeRange: %v", err)
	}
	// DuckDB stores ISO strings as TIMESTAMP and CAST AS VARCHAR uses
	// space-separated `YYYY-MM-DD HH:MM:SS`.
	if tr.Min == nil || *tr.Min != "2026-05-20 09:00:00" {
		t.Errorf("min = %v, want 2026-05-20 09:00:00", deref(tr.Min))
	}
	if tr.Max == nil || *tr.Max != "2026-05-20 13:00:00" {
		t.Errorf("max = %v, want 2026-05-20 13:00:00", deref(tr.Max))
	}

	// Disable file b; range collapses to file a's bounds.
	for _, fi := range eng.GetFiles() {
		if filepath.Base(fi.Path) == "b.ndjson" {
			if err := eng.SetFileEnabled(fi.ID, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	tr, err = eng.GetTimeRange()
	if err != nil {
		t.Fatalf("GetTimeRange after disable: %v", err)
	}
	if *tr.Min != "2026-05-20 10:00:00" || *tr.Max != "2026-05-20 12:00:00" {
		t.Errorf("after disable: min=%v max=%v", deref(tr.Min), deref(tr.Max))
	}
}

func TestSetFileEnabledFiltersQuery(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.ndjson")
	b := filepath.Join(dir, "b.ndjson")
	mustWriteJSON(t, a, map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "src": "A"})
	mustWriteJSON(t, b, map[string]any{"@timestamp": "2026-05-20T10:00:01Z", "src": "B"})
	eng, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	for _, p := range []string{a, b} {
		if err := eng.LoadFile(p); err != nil {
			t.Fatalf("load %s: %v", p, err)
		}
	}

	all, _ := eng.Query(QueryRequest{Limit: 10})
	if all.TotalCount != 2 {
		t.Errorf("both enabled: count=%d, want 2", all.TotalCount)
	}

	for _, fi := range eng.GetFiles() {
		if filepath.Base(fi.Path) == "b.ndjson" {
			_ = eng.SetFileEnabled(fi.ID, false)
		}
	}
	only, _ := eng.Query(QueryRequest{Limit: 10})
	if only.TotalCount != 1 {
		t.Errorf("b disabled: count=%d, want 1", only.TotalCount)
	}
	if only.Rows[0]["src"] != "A" {
		t.Errorf("expected only A, got %v", only.Rows[0])
	}
}

func TestUnloadFileRemovesData(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.ndjson")
	mustWriteJSON(t, a, map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "x": 1})
	eng, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if err := eng.LoadFile(a); err != nil {
		t.Fatal(err)
	}
	id := eng.GetFiles()[0].ID
	if err := eng.UnloadFile(id); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	if got := len(eng.GetFiles()); got != 0 {
		t.Errorf("expected 0 files after unload, got %d", got)
	}
	res, _ := eng.Query(QueryRequest{Limit: 10})
	if res.TotalCount != 0 {
		t.Errorf("expected empty after unload, got %d rows", res.TotalCount)
	}
	// And we can re-load the same path now that the index is cleared.
	if err := eng.LoadFile(a); err != nil {
		t.Errorf("re-load after unload failed: %v", err)
	}
}

func TestDuplicateLoadRejected(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.ndjson")
	mustWriteJSON(t, a, map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "x": 1})
	eng, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if err := eng.LoadFile(a); err != nil {
		t.Fatal(err)
	}
	err = eng.LoadFile(a)
	if err == nil {
		t.Fatalf("expected duplicate-load error, got nil")
	}
	if !strings.Contains(err.Error(), "already loaded") {
		t.Errorf("expected 'already loaded' message, got %v", err)
	}
}

func TestUnloadUnknownIDErrors(t *testing.T) {
	eng, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if err := eng.UnloadFile("does-not-exist"); err == nil {
		t.Errorf("expected error unloading unknown id")
	}
}

func TestQueryDefaultsLimitAndSort(t *testing.T) {
	// Limit defaults to 100, SortOrder defaults to "asc".
	var recs []map[string]any
	for i := 0; i < 150; i++ {
		recs = append(recs, map[string]any{
			"@timestamp": "2026-05-20T10:00:" + zeropad2(i%60) + "Z",
			"n":          i,
		})
	}
	eng := loadOne(t, recs...)
	res, err := eng.Query(QueryRequest{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Limit != 100 {
		t.Errorf("default limit = %d, want 100", res.Limit)
	}
	if len(res.Rows) != 100 {
		t.Errorf("default page size = %d rows, want 100", len(res.Rows))
	}
}

func TestExportFilteredWritesNDJSON(t *testing.T) {
	eng := loadOne(t,
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "level": "INFO", "msg": "a"},
		map[string]any{"@timestamp": "2026-05-20T10:00:01Z", "level": "ERROR", "msg": "b"},
		map[string]any{"@timestamp": "2026-05-20T10:00:02Z", "level": "INFO", "msg": "c"},
	)
	out := filepath.Join(t.TempDir(), "out.ndjson")
	n, err := eng.ExportFiltered(QueryRequest{
		Filters: []Filter{{Field: "level", Operator: "is", Value: "INFO"}},
	}, out)
	if err != nil {
		t.Fatalf("ExportFiltered: %v", err)
	}
	if n != 2 {
		t.Errorf("exported records=%d, want 2", n)
	}
}

func TestFieldSamplesCapsAndDrops(t *testing.T) {
	// One low-cardinality field (kept), one high-cardinality field
	// (returned as empty slice — signal to caller "drop this").
	var recs []map[string]any
	for i := 0; i < 50; i++ {
		recs = append(recs, map[string]any{
			"@timestamp": "2026-05-20T10:00:00Z",
			"low":        "L" + zeropad2(i%3), // 3 values
			"high":       "H" + zeropad2(i),   // 50 values
		})
	}
	eng := loadOne(t, recs...)
	samples, err := eng.FieldSamples([]string{"low", "high", "absent"}, 10)
	if err != nil {
		t.Fatalf("FieldSamples: %v", err)
	}
	if got, ok := samples["low"]; !ok || len(got) != 3 {
		t.Errorf("low: got %v, want 3 distinct values", got)
	}
	if got, ok := samples["high"]; !ok || len(got) != 0 {
		t.Errorf("high: got %v, want empty slice (exceeded cap)", got)
	}
	if _, ok := samples["absent"]; ok {
		t.Errorf("absent: should be omitted, got entry")
	}
}

// ---- helpers ----

func zeropad2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func containsStr(slice []string, want string) bool {
	for _, s := range slice {
		if s == want {
			return true
		}
	}
	return false
}

func deref(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// --- Histogram (#19) ---

func strPtr(s string) *string { return &s }

func TestHistogramBucketsMatchingRecords(t *testing.T) {
	eng := loadOne(t,
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "level": "INFO"},
		map[string]any{"@timestamp": "2026-05-20T10:00:30Z", "level": "INFO"},
		map[string]any{"@timestamp": "2026-05-20T10:01:10Z", "level": "WARN"},
		map[string]any{"@timestamp": "2026-05-20T10:02:00Z", "level": "ERROR"},
	)
	h, err := eng.GetHistogram(QueryRequest{})
	if err != nil {
		t.Fatalf("GetHistogram: %v", err)
	}
	if h.Total != 4 {
		t.Errorf("total = %d, want 4", h.Total)
	}
	if len(h.Buckets) == 0 {
		t.Fatal("no buckets")
	}
	// Bars must stay bounded whatever the span — that cap is what keeps
	// an aggregate that runs on every query cheap.
	if len(h.Buckets) > maxHistogramBuckets {
		t.Errorf("%d buckets exceeds the cap of %d", len(h.Buckets), maxHistogramBuckets)
	}
	var sum int64
	for _, b := range h.Buckets {
		sum += b.Count
	}
	if sum != h.Total {
		t.Errorf("buckets sum to %d but total is %d", sum, h.Total)
	}
	if h.Field != "@timestamp" {
		t.Errorf("field = %q", h.Field)
	}
}

// The strip has to describe the result on screen, so it takes the same
// filters as the query. A histogram of everything next to a filtered
// table would be actively misleading.
func TestHistogramRespectsFilters(t *testing.T) {
	eng := loadOne(t,
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "level": "INFO"},
		map[string]any{"@timestamp": "2026-05-20T10:00:30Z", "level": "INFO"},
		map[string]any{"@timestamp": "2026-05-20T10:01:10Z", "level": "ERROR"},
	)
	h, err := eng.GetHistogram(QueryRequest{
		Filters: []Filter{{Field: "level", Operator: "is", Value: "ERROR"}},
	})
	if err != nil {
		t.Fatalf("GetHistogram: %v", err)
	}
	if h.Total != 1 {
		t.Errorf("total = %d, want 1 (filters ignored?)", h.Total)
	}
}

// Narrowing the time range must re-resolve the bucket width. Keeping a
// width derived from the whole data set would collapse a five-minute
// window into a single bar that answers nothing.
func TestHistogramIntervalFollowsTheFilteredSpan(t *testing.T) {
	eng := loadOne(t,
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z"},
		map[string]any{"@timestamp": "2026-05-20T10:00:10Z"},
		map[string]any{"@timestamp": "2026-05-20T10:00:20Z"},
		map[string]any{"@timestamp": "2026-11-20T10:00:00Z"},
	)
	wide, err := eng.GetHistogram(QueryRequest{})
	if err != nil {
		t.Fatal(err)
	}
	narrow, err := eng.GetHistogram(QueryRequest{
		TimeFrom: strPtr("2026-05-20T10:00:00Z"),
		TimeTo:   strPtr("2026-05-20T10:00:30Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if narrow.IntervalSeconds >= wide.IntervalSeconds {
		t.Errorf("narrow interval %ds is not finer than the wide one %ds",
			narrow.IntervalSeconds, wide.IntervalSeconds)
	}
	if narrow.Total != 3 {
		t.Errorf("narrow total = %d, want 3", narrow.Total)
	}
}

// A file with no usable timestamp must produce an empty strip, not an
// error: the histogram can never be the reason a query fails.
func TestHistogramWithoutTimestampsIsEmptyNotAnError(t *testing.T) {
	eng := loadOne(t,
		map[string]any{"name": "a", "value": 1},
		map[string]any{"name": "b", "value": 2},
	)
	h, err := eng.GetHistogram(QueryRequest{})
	if err != nil {
		t.Fatalf("GetHistogram returned an error: %v", err)
	}
	if len(h.Buckets) != 0 {
		t.Errorf("got %d buckets from data with no timestamps", len(h.Buckets))
	}
}

func TestHistogramEmptyEngine(t *testing.T) {
	eng, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	h, err := eng.GetHistogram(QueryRequest{})
	if err != nil {
		t.Fatalf("GetHistogram: %v", err)
	}
	if len(h.Buckets) != 0 || h.Total != 0 {
		t.Errorf("expected an empty histogram, got %+v", h)
	}
}

func TestPickHistogramIntervalStaysUnderTheCap(t *testing.T) {
	for _, span := range []time.Duration{
		time.Second, 30 * time.Second, 5 * time.Minute, time.Hour,
		24 * time.Hour, 30 * 24 * time.Hour, 365 * 24 * time.Hour,
		10 * 365 * 24 * time.Hour,
	} {
		iv := pickHistogramInterval(span)
		if iv <= 0 {
			t.Errorf("span %s produced a non-positive interval %d", span, iv)
			continue
		}
		if bars := int64(span.Seconds()) / iv; bars > maxHistogramBuckets {
			t.Errorf("span %s would render %d bars with a %ds interval", span, bars, iv)
		}
	}
}

// A time bound may arrive from a shared URL or another tool, not just
// from the one input widget that used to be its only source. Every
// spelling of the same instant has to select the same rows — the
// comparison used to be lexicographic against the VARCHAR-rendered
// column, so an ISO-8601 bound with a 'T' silently matched nothing.
func TestTimeFilterAcceptsEverySpellingOfAnInstant(t *testing.T) {
	eng := loadOne(t,
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z"},
		map[string]any{"@timestamp": "2026-05-20T10:00:10Z"},
		map[string]any{"@timestamp": "2026-05-20T10:00:20Z"},
		map[string]any{"@timestamp": "2026-11-20T10:00:00Z"},
	)
	for _, pair := range [][2]string{
		{"2026-05-20 10:00:00", "2026-05-20 10:00:30"},
		{"2026-05-20T10:00:00", "2026-05-20T10:00:30"},
		{"2026-05-20T10:00:00Z", "2026-05-20T10:00:30Z"},
		{"2026-05-20T10:00:00.000Z", "2026-05-20T10:00:30.000Z"},
	} {
		from, to := pair[0], pair[1]
		res, err := eng.Query(QueryRequest{Limit: 10, TimeFrom: &from, TimeTo: &to})
		if err != nil {
			t.Errorf("%q..%q: %v", from, to, err)
			continue
		}
		if res.TotalCount != 3 {
			t.Errorf("%q..%q matched %d rows, want 3", from, to, res.TotalCount)
		}
	}
}

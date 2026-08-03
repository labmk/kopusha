package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labmk/kopusha/internal/engine"
	"github.com/labmk/kopusha/internal/settings"
)

// newTestServerDirect builds a Server struct directly, skipping New().
// Reason: New() requires an embed.FS containing a "static/" subdir for
// the SPA catch-all. The handlers under test are all under /api/* —
// the SPA route is irrelevant — so we register the API mux manually
// and avoid having to produce a real frontend embed at test time.
func newTestServerDirect(t *testing.T) (*httptest.Server, *Server, *engine.Engine, string) {
	t.Helper()
	eng, err := engine.New()
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	tmpDir := t.TempDir()
	store := settings.NewStore(tmpDir)
	if err := store.Load(); err != nil {
		t.Fatalf("settings.Load: %v", err)
	}

	srv := &Server{
		eng:          eng,
		mux:          http.NewServeMux(),
		version:      "test-version",
		settings:     store,
		lastActivity: time.Now(),
	}
	// Register only API routes — skip the SPA static catch-all so we
	// don't need an embed.FS with a "static/" subdir.
	srv.mux.HandleFunc("/api/version", srv.APIHandler(srv.handleVersion))
	srv.mux.HandleFunc("/api/files", srv.APIHandler(srv.handleFiles))
	srv.mux.HandleFunc("/api/files/load", srv.APIHandler(srv.handleLoadFile))
	srv.mux.HandleFunc("/api/files/unload", srv.APIHandler(srv.handleUnloadFile))
	srv.mux.HandleFunc("/api/files/toggle", srv.APIHandler(srv.handleToggleFile))
	srv.mux.HandleFunc("/api/browse", srv.APIHandler(srv.handleBrowse))
	srv.mux.HandleFunc("/api/query", srv.APIHandler(srv.handleQuery))
	srv.mux.HandleFunc("/api/fields", srv.APIHandler(srv.handleFields))
	srv.mux.HandleFunc("/api/timerange", srv.APIHandler(srv.handleTimeRange))
	srv.mux.HandleFunc("/api/timestamp-field", srv.APIHandler(srv.handleTimestampField))
	srv.mux.HandleFunc("/api/export", srv.APIHandler(srv.handleExport))
	srv.mux.HandleFunc("/api/settings", srv.APIHandler(srv.handleSettings))
	srv.mux.HandleFunc("/api/files/load-dir", srv.APIHandler(srv.handleLoadDir))
	srv.mux.HandleFunc("/api/field-samples", srv.APIHandler(srv.handleFieldSamples))
	srv.mux.HandleFunc("/api/shutdown", srv.handleShutdown)

	srvHTTP := httptest.NewServer(srv.mux)
	t.Cleanup(srvHTTP.Close)
	return srvHTTP, srv, eng, tmpDir
}

func mustNDJSON(t *testing.T, dir, name string, recs ...map[string]any) string {
	t.Helper()
	path := filepath.Join(dir, name)
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
	return path
}

func mustPostJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(body); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", buf)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response, into any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// ---- /api/version ----

func TestVersionHandler(t *testing.T) {
	srv, server, _, _ := newTestServerDirect(t)
	server.SetIdleTimeoutSeconds(180)

	resp, err := http.Get(srv.URL + "/api/version")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d", resp.StatusCode)
	}
	var v map[string]any
	decode(t, resp, &v)
	if v["version"] != "test-version" {
		t.Errorf("version=%v", v["version"])
	}
	// Comes through as float64 via JSON.
	if v["idle_timeout_seconds"].(float64) != 180 {
		t.Errorf("idle_timeout_seconds=%v", v["idle_timeout_seconds"])
	}
	if v["os"] == nil || v["arch"] == nil {
		t.Errorf("missing os/arch: %v", v)
	}
}

// ---- /api/files lifecycle ----

func TestFilesLoadListToggleUnload(t *testing.T) {
	srv, _, eng, _ := newTestServerDirect(t)
	dir := t.TempDir()
	path := mustNDJSON(t, dir, "x.ndjson",
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "m": "a"},
		map[string]any{"@timestamp": "2026-05-20T10:00:01Z", "m": "b"},
	)

	// Load
	resp := mustPostJSON(t, srv.URL+"/api/files/load", map[string]string{"path": path})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("load status=%d", resp.StatusCode)
	}
	resp.Body.Close()

	// List
	resp, err := http.Get(srv.URL + "/api/files")
	if err != nil {
		t.Fatal(err)
	}
	var listResp struct {
		Files          []engine.FileInfo `json:"files"`
		TimestampField string            `json:"timestamp_field"`
	}
	decode(t, resp, &listResp)
	if len(listResp.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(listResp.Files))
	}
	if listResp.TimestampField != "@timestamp" {
		t.Errorf("timestamp_field=%q", listResp.TimestampField)
	}
	id := listResp.Files[0].ID

	// Toggle off → query returns empty
	resp = mustPostJSON(t, srv.URL+"/api/files/toggle", map[string]any{"id": id, "enabled": false})
	resp.Body.Close()
	res, err := eng.Query(engine.QueryRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalCount != 0 {
		t.Errorf("expected 0 after toggle off, got %d", res.TotalCount)
	}

	// Unload
	resp = mustPostJSON(t, srv.URL+"/api/files/unload", map[string]string{"id": id})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("unload status=%d", resp.StatusCode)
	}
	resp.Body.Close()
	if got := len(eng.GetFiles()); got != 0 {
		t.Errorf("after unload: %d files", got)
	}
}

func TestFilesLoadRejectsMissingPath(t *testing.T) {
	srv, _, _, _ := newTestServerDirect(t)
	resp := mustPostJSON(t, srv.URL+"/api/files/load", map[string]string{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing path: status=%d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestFilesLoadRejectsBadJSON(t *testing.T) {
	srv, _, _, _ := newTestServerDirect(t)
	resp, err := http.Post(srv.URL+"/api/files/load", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad json: status=%d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestFilesLoadRejectsWrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServerDirect(t)
	resp, err := http.Get(srv.URL + "/api/files/load")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET on POST endpoint: status=%d, want 405", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestFilesUnloadUnknownID(t *testing.T) {
	srv, _, _, _ := newTestServerDirect(t)
	resp := mustPostJSON(t, srv.URL+"/api/files/unload", map[string]string{"id": "nope"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown id: status=%d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

// ---- /api/query ----

func TestQueryHandlerEndToEnd(t *testing.T) {
	srv, _, eng, _ := newTestServerDirect(t)
	dir := t.TempDir()
	path := mustNDJSON(t, dir, "q.ndjson",
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "level": "INFO", "m": "a"},
		map[string]any{"@timestamp": "2026-05-20T10:00:01Z", "level": "ERROR", "m": "b"},
		map[string]any{"@timestamp": "2026-05-20T10:00:02Z", "level": "WARN", "m": "c"},
	)
	if err := eng.LoadFile(path); err != nil {
		t.Fatal(err)
	}

	// Filter on level=ERROR via the HTTP layer (round-trips through
	// engine.QueryRequest JSON).
	body := map[string]any{
		"filters": []map[string]string{{"field": "level", "operator": "is", "value": "ERROR"}},
		"limit":   10,
	}
	resp := mustPostJSON(t, srv.URL+"/api/query", body)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var q struct {
		Rows       []map[string]any `json:"rows"`
		TotalCount int64            `json:"total_count"`
	}
	decode(t, resp, &q)
	if q.TotalCount != 1 {
		t.Errorf("count=%d, want 1", q.TotalCount)
	}
	if len(q.Rows) != 1 || q.Rows[0]["m"] != "b" {
		t.Errorf("rows=%+v", q.Rows)
	}
}

func TestQueryHandlerRejectsBadJSON(t *testing.T) {
	srv, _, _, _ := newTestServerDirect(t)
	resp, err := http.Post(srv.URL+"/api/query", "application/json", strings.NewReader("{bad"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad json: status=%d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestQueryHandlerRejectsWrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServerDirect(t)
	resp, err := http.Get(srv.URL + "/api/query")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status=%d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ---- /api/fields and /api/timerange ----

func TestFieldsAndTimeRangeAfterLoad(t *testing.T) {
	srv, _, eng, _ := newTestServerDirect(t)
	dir := t.TempDir()
	path := mustNDJSON(t, dir, "ft.ndjson",
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "x": 1, "y": "a"},
		map[string]any{"@timestamp": "2026-05-20T10:00:01Z", "x": 2, "y": "b"},
	)
	if err := eng.LoadFile(path); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL + "/api/fields")
	if err != nil {
		t.Fatal(err)
	}
	var fr struct {
		Fields []string `json:"fields"`
	}
	decode(t, resp, &fr)
	want := map[string]bool{"@timestamp": true, "x": true, "y": true}
	got := map[string]bool{}
	for _, f := range fr.Fields {
		got[f] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing field %q in %v", k, fr.Fields)
		}
	}

	resp, err = http.Get(srv.URL + "/api/timerange")
	if err != nil {
		t.Fatal(err)
	}
	var tr struct {
		Min    *string  `json:"min"`
		Max    *string  `json:"max"`
		Fields []string `json:"timestamp_fields"`
	}
	decode(t, resp, &tr)
	if tr.Min == nil || tr.Max == nil {
		t.Errorf("nil min/max")
	}
}

// ---- /api/timestamp-field ----

func TestTimestampFieldGetSet(t *testing.T) {
	srv, _, eng, _ := newTestServerDirect(t)

	// GET
	resp, err := http.Get(srv.URL + "/api/timestamp-field")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	decode(t, resp, &got)
	if got["field"] != "@timestamp" {
		t.Errorf("default field=%q", got["field"])
	}

	// POST override
	resp = mustPostJSON(t, srv.URL+"/api/timestamp-field", map[string]string{"field": "ObservedTimestamp"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST status=%d", resp.StatusCode)
	}
	resp.Body.Close()
	if eng.GetTimestampField() != "ObservedTimestamp" {
		t.Errorf("engine timestamp = %q, want ObservedTimestamp", eng.GetTimestampField())
	}

	// Bogus method
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/timestamp-field", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: status=%d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ---- /api/browse path containment ----

func TestBrowseListsDirectory(t *testing.T) {
	srv, _, _, _ := newTestServerDirect(t)
	dir := t.TempDir()
	// Drop one ingestable + one non-ingestable file so we can confirm
	// the extension filter is applied.
	_ = mustNDJSON(t, dir, "data.ndjson",
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "k": "v"})
	if err := os.WriteFile(filepath.Join(dir, "binary.dat"), []byte("xx"), 0644); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL + "/api/browse?path=" + dir)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var br struct {
		Entries []struct {
			Name  string `json:"name"`
			IsDir bool   `json:"is_dir"`
		} `json:"entries"`
	}
	decode(t, resp, &br)
	names := map[string]bool{}
	for _, e := range br.Entries {
		names[e.Name] = true
	}
	if !names["data.ndjson"] {
		t.Errorf("expected data.ndjson in entries, got %v", names)
	}
	if names["binary.dat"] {
		t.Errorf("binary.dat should be hidden by ingestableExts: %v", names)
	}
}

func TestBrowseRejectsUnreadablePath(t *testing.T) {
	srv, _, _, _ := newTestServerDirect(t)
	// Non-existent path — server returns 400 on non-Windows, may fall
	// back to C:\ on Windows. Assert on the response shape rather than
	// the status to keep the test cross-platform.
	resp, err := http.Get(srv.URL + "/api/browse?path=/this/path/does/not/exist/at-all")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// Either 200 (Windows fallback to C:\) or 400 (other OS); both
	// indicate the handler handled the bad path without panicking.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 200 or 400", resp.StatusCode)
	}
}

// ---- /api/settings ----

func TestSettingsRoundtrip(t *testing.T) {
	srv, _, _, _ := newTestServerDirect(t)

	// GET defaults — at minimum should return the DefaultLastDirectory.
	resp, err := http.Get(srv.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	resp.Body.Close()

	// POST update.
	dir := t.TempDir()
	resp = mustPostJSON(t, srv.URL+"/api/settings", map[string]any{
		"last_directory": dir,
	})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST status=%d", resp.StatusCode)
	}
	resp.Body.Close()

	// GET again — directory persisted.
	resp, err = http.Get(srv.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	decode(t, resp, &s)
	if s["last_directory"] != dir {
		t.Errorf("last_directory=%v, want %s", s["last_directory"], dir)
	}
}

func TestSettingsRejectsBadJSON(t *testing.T) {
	srv, _, _, _ := newTestServerDirect(t)
	resp, err := http.Post(srv.URL+"/api/settings", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad json: status=%d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ---- /api/files/load-dir ----

func TestLoadDirLoadsEveryFile(t *testing.T) {
	srv, _, eng, _ := newTestServerDirect(t)
	dir := t.TempDir()
	for i, name := range []string{"a.ndjson", "b.ndjson"} {
		_ = mustNDJSON(t, dir, name, map[string]any{
			"@timestamp": fmt.Sprintf("2026-05-20T10:00:0%dZ", i),
			"n":          i,
		})
	}

	resp := mustPostJSON(t, srv.URL+"/api/files/load-dir", map[string]string{"path": dir})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var loadDir struct {
		Loaded []string `json:"loaded"`
		Errors []string `json:"errors"`
	}
	decode(t, resp, &loadDir)
	if len(loadDir.Loaded) != 2 {
		t.Errorf("loaded=%v, want 2", loadDir.Loaded)
	}
	if len(loadDir.Errors) != 0 {
		t.Errorf("unexpected errors: %v", loadDir.Errors)
	}
	if got := len(eng.GetFiles()); got != 2 {
		t.Errorf("engine has %d files, want 2", got)
	}
}

// ---- /api/shutdown grace cancellation ----

func TestShutdownGraceCancelledByActivity(t *testing.T) {
	srv, server, _, _ := newTestServerDirect(t)

	// Capture LastActivity before shutdown.
	before := server.LastActivity()

	resp := mustPostJSON(t, srv.URL+"/api/shutdown", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d", resp.StatusCode)
	}
	resp.Body.Close()

	// Within the 2-second grace window, hit /api/version. That bumps
	// LastActivity past `before`, which the grace goroutine reads to
	// decide whether to call os.Exit(0).
	time.Sleep(200 * time.Millisecond)
	resp2, err := http.Get(srv.URL + "/api/version")
	if err != nil {
		t.Fatalf("version during grace: %v", err)
	}
	resp2.Body.Close()

	// Wait past the grace window. If the cancellation logic is broken,
	// the os.Exit(0) at the end of handleShutdown would have killed
	// the test process. Reaching here without dying = grace cancelled.
	time.Sleep(2200 * time.Millisecond)

	after := server.LastActivity()
	if !after.After(before) {
		t.Errorf("LastActivity didn't advance: before=%v after=%v", before, after)
	}
}

func TestShutdownRejectsWrongMethod(t *testing.T) {
	srv, _, _, _ := newTestServerDirect(t)
	resp, err := http.Get(srv.URL + "/api/shutdown")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status=%d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ---- /api/field-samples ----

func TestFieldSamplesHandler(t *testing.T) {
	srv, _, eng, _ := newTestServerDirect(t)
	dir := t.TempDir()
	path := mustNDJSON(t, dir, "fs.ndjson",
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "level": "INFO"},
		map[string]any{"@timestamp": "2026-05-20T10:00:01Z", "level": "WARN"},
		map[string]any{"@timestamp": "2026-05-20T10:00:02Z", "level": "INFO"},
	)
	if err := eng.LoadFile(path); err != nil {
		t.Fatal(err)
	}

	// Explicit fields param.
	resp, err := http.Get(srv.URL + "/api/field-samples?fields=level&cap=10")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var out map[string][]string
	decode(t, resp, &out)
	got := out["level"]
	if len(got) != 2 {
		t.Errorf("level distinct=%v, want 2", got)
	}
}

// ---- /api/export ----

func TestExportRequiresOutputPath(t *testing.T) {
	srv, _, _, _ := newTestServerDirect(t)
	resp := mustPostJSON(t, srv.URL+"/api/export", map[string]any{
		"query":       map[string]any{"limit": 10},
		"output_path": "",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestExportWritesNDJSON(t *testing.T) {
	srv, _, eng, _ := newTestServerDirect(t)
	dir := t.TempDir()
	path := mustNDJSON(t, dir, "src.ndjson",
		map[string]any{"@timestamp": "2026-05-20T10:00:00Z", "k": "v1"},
		map[string]any{"@timestamp": "2026-05-20T10:00:01Z", "k": "v2"},
	)
	if err := eng.LoadFile(path); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.ndjson")
	resp := mustPostJSON(t, srv.URL+"/api/export", map[string]any{
		"query":       map[string]any{"limit": 100},
		"output_path": out,
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var exp struct {
		Records int64  `json:"records"`
		Path    string `json:"path"`
	}
	decode(t, resp, &exp)
	if exp.Records != 2 {
		t.Errorf("records=%d, want 2", exp.Records)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("output not written: %v", err)
	}
}

// ---- APIHandler activity touch ----

func TestAPIHandlerTouchesActivity(t *testing.T) {
	srv, server, _, _ := newTestServerDirect(t)

	// Force last-activity backwards.
	server.activityMu.Lock()
	server.lastActivity = time.Now().Add(-1 * time.Hour)
	old := server.lastActivity
	server.activityMu.Unlock()

	resp, err := http.Get(srv.URL + "/api/files")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !server.LastActivity().After(old) {
		t.Errorf("LastActivity not advanced after wrapped handler call")
	}
}

// ---- IsBusy + AddBusyCheck ----

func TestIsBusyConsultsRegisteredChecks(t *testing.T) {
	_, server, _, _ := newTestServerDirect(t)
	if server.IsBusy() {
		t.Fatalf("fresh server should not be busy")
	}
	busy := false
	server.AddBusyCheck(func() bool { return busy })
	if server.IsBusy() {
		t.Fatalf("check returns false → not busy")
	}
	busy = true
	if !server.IsBusy() {
		t.Errorf("check returns true → should be busy")
	}
}

// ---- Zip helper unit tests (exported indirectly through handleBrowse, but
//      simpler to test directly here) ----

func TestZipBaseNameHandlesBothSeparators(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo.ndjson", "foo.ndjson"},
		{"dir/foo.ndjson", "foo.ndjson"},
		{`dir\foo.ndjson`, "foo.ndjson"},
		{`a/b/c\d.ndjson`, "d.ndjson"},
		{"", ""},
	}
	for _, c := range cases {
		if got := zipBaseName(c.in); got != c.want {
			t.Errorf("zipBaseName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsZipVirtPath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{`C:\foo.zip|inner.ndjson`, true},
		{`/path/to/snap.zip|metrics/logs.ndjson`, true},
		{`/path/to/no-pipe.zip`, false},
		{`/path/to/foo.txt|bar`, false}, // pipe but not .zip
		{`plain/path.ndjson`, false},
	}
	for _, c := range cases {
		if got := isZipVirtPath(c.in); got != c.want {
			t.Errorf("isZipVirtPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSplitZipVirt(t *testing.T) {
	z, inner := splitZipVirt(`C:\foo.zip|metrics.ndjson`)
	if z != `C:\foo.zip` || inner != "metrics.ndjson" {
		t.Errorf("split = (%q, %q)", z, inner)
	}
	z, inner = splitZipVirt("no-pipe")
	if z != "no-pipe" || inner != "" {
		t.Errorf("no-pipe split = (%q, %q)", z, inner)
	}
}

func TestIsIngestableExt(t *testing.T) {
	good := []string{"a.ndjson", "x.NDJSON", "z.evtx", "y.xml", "log.log", "stuff.txt", "out.out", "data.csv", "f.json", "ar.zip"}
	bad := []string{"a", "a.dat", "a.exe", "a.png", "a.tar.gz", "a.pdf"}
	for _, g := range good {
		if !isIngestableExt(g) {
			t.Errorf("isIngestableExt(%q) = false, want true", g)
		}
	}
	for _, b := range bad {
		if isIngestableExt(b) {
			t.Errorf("isIngestableExt(%q) = true, want false", b)
		}
	}
}

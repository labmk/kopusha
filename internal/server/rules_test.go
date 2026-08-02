package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labmk/obs-viewer/internal/engine"
	"github.com/labmk/obs-viewer/internal/parsers"
	"github.com/labmk/obs-viewer/internal/settings"
)

// newRuleTestServer wires the rule routes over a real parsers.Manager
// with an empty rules directory, so a test starts from "nothing is
// recognized" — which is the state the whole feature exists for.
func newRuleTestServer(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()
	eng, err := engine.New()
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	tmpDir := t.TempDir()
	rulesDir := filepath.Join(tmpDir, "parsers.d")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := settings.NewStore(tmpDir)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}

	mgr := parsers.NewManager(rulesDir, eng.SetLoaders)
	if _, err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}

	srv := &Server{
		eng:          eng,
		mux:          http.NewServeMux(),
		version:      "test-version",
		settings:     store,
		lastActivity: time.Now(),
		rules:        mgr,
	}
	srv.mux.HandleFunc("/api/files/load", srv.APIHandler(srv.handleLoadFile))
	srv.registerRuleRoutes()

	srvHTTP := httptest.NewServer(srv.mux)
	t.Cleanup(srvHTTP.Close)
	return srvHTTP, tmpDir, rulesDir
}

const gatewaySample = `2026-03-18T06:00:00 gateway[4179]: queue depth 2347
2026-03-18T06:00:04 gateway[4179]: queue depth 2412
2026-03-18T06:00:09 gateway[8802]: rebalanced shard 7
`

// The whole point of the pair: a file that nothing recognizes has to
// produce an explanation, and that explanation has to be the input the
// rule builder needs.
func TestExplainReportsEveryAdapter(t *testing.T) {
	srvHTTP, tmpDir, _ := newRuleTestServer(t)
	path := filepath.Join(tmpDir, "gateway.log")
	if err := os.WriteFile(path, []byte(gatewaySample), 0o644); err != nil {
		t.Fatal(err)
	}

	// It must genuinely not load, or the test is not exercising the
	// case the feature is for.
	resp := mustPostJSON(t, srvHTTP.URL+"/api/files/load", map[string]string{"path": path})
	if resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		t.Fatal("the sample loaded; it is supposed to match no rule")
	}
	resp.Body.Close()

	resp = mustPostJSON(t, srvHTTP.URL+"/api/files/explain", map[string]string{"path": path})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("explain returned %d", resp.StatusCode)
	}
	var d struct {
		Chosen    string `json:"chosen"`
		BestScore int    `json:"best_score"`
		Adapters  []struct {
			Name   string `json:"name"`
			Score  int    `json:"score"`
			Reason string `json:"reason"`
		} `json:"adapters"`
		FirstLine string   `json:"first_line"`
		Notes     []string `json:"notes"`
	}
	decode(t, resp, &d)

	if d.Chosen != "" || d.BestScore != 0 {
		t.Errorf("chosen = %q score = %d, want nothing matched", d.Chosen, d.BestScore)
	}
	// Every registered adapter must appear, including the ones that
	// declined — those are the whole point.
	want := map[string]bool{"ndjson": false, "parquet": false, "evtx": false, "block": false, "line": false, "xml": false}
	for _, a := range d.Adapters {
		if _, ok := want[a.Name]; !ok {
			t.Errorf("unexpected adapter %q", a.Name)
			continue
		}
		want[a.Name] = true
		if a.Reason == "" {
			t.Errorf("adapter %q gave a score with no reason", a.Name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("adapter %q missing from the diagnosis", name)
		}
	}
	if !strings.Contains(d.FirstLine, "queue depth 2347") {
		t.Errorf("first_line = %q, want the first line of the file", d.FirstLine)
	}
}

// Encoding traits break matching invisibly, so they have to be called
// out by name rather than left for the operator to discover.
func TestExplainReportsEncodingTraits(t *testing.T) {
	srvHTTP, tmpDir, _ := newRuleTestServer(t)
	path := filepath.Join(tmpDir, "windows.log")
	content := "\xef\xbb\xbf2026-03-18T06:00:00 first\r\n2026-03-18T06:00:04 second\r\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := mustPostJSON(t, srvHTTP.URL+"/api/files/explain", map[string]string{"path": path})
	defer resp.Body.Close()
	var d struct {
		FirstLine string   `json:"first_line"`
		Notes     []string `json:"notes"`
	}
	decode(t, resp, &d)

	joined := strings.Join(d.Notes, " | ")
	if !strings.Contains(joined, "byte-order mark") {
		t.Errorf("notes do not mention the BOM: %q", joined)
	}
	if !strings.Contains(joined, "CRLF") {
		t.Errorf("notes do not mention CRLF: %q", joined)
	}
	// The reported line is what the parser sees, so the BOM and the CR
	// must already be gone from it.
	if strings.HasPrefix(d.FirstLine, "\xef\xbb\xbf") || strings.HasSuffix(d.FirstLine, "\r") {
		t.Errorf("first_line still carries the BOM or CR: %q", d.FirstLine)
	}
}

// The loop the feature exists for: a file that will not load, a rule
// built from its first line, and the same file loading afterwards
// without a restart.
func TestRuleBuilderRoundTrip(t *testing.T) {
	srvHTTP, tmpDir, rulesDir := newRuleTestServer(t)
	path := filepath.Join(tmpDir, "gateway.log")
	if err := os.WriteFile(path, []byte(gatewaySample), 0o644); err != nil {
		t.Fatal(err)
	}

	// Suggest.
	resp := mustPostJSON(t, srvHTTP.URL+"/api/rules/suggest", map[string]string{"sample": gatewaySample})
	var draft map[string]any
	decode(t, resp, &draft)
	resp.Body.Close()
	if draft["parse"] == "" || draft["ts_layout"] == "" {
		t.Fatalf("suggest produced nothing usable: %#v", draft)
	}

	// Preview, through the real adapter.
	resp = mustPostJSON(t, srvHTTP.URL+"/api/rules/preview", map[string]any{
		"rule": draft, "sample": gatewaySample,
	})
	var prev struct {
		Fields          []string         `json:"fields"`
		Rows            []map[string]any `json:"rows"`
		Parsed          int              `json:"parsed"`
		Continuation    int              `json:"continuation"`
		TimestampErrors int              `json:"timestamp_errors"`
		Error           string           `json:"error"`
	}
	decode(t, resp, &prev)
	resp.Body.Close()
	if prev.Error != "" {
		t.Fatalf("preview error: %s", prev.Error)
	}
	if prev.Parsed != 3 || prev.Continuation != 0 {
		t.Errorf("parsed=%d continuation=%d, want 3 and 0", prev.Parsed, prev.Continuation)
	}
	if prev.TimestampErrors != 0 {
		t.Errorf("%d rows would load without a timestamp", prev.TimestampErrors)
	}
	if len(prev.Rows) != 3 {
		t.Errorf("preview returned %d rows, want 3", len(prev.Rows))
	}

	// Save.
	draft["name"] = "Gateway Log"
	resp = mustPostJSON(t, srvHTTP.URL+"/api/rules/save", map[string]any{"rule": draft})
	var saved struct {
		Status string `json:"status"`
		File   string `json:"file"`
		Rules  int    `json:"rules"`
	}
	decode(t, resp, &saved)
	resp.Body.Close()
	if saved.Status != "ok" {
		t.Fatalf("save failed: %#v", saved)
	}
	if _, err := os.Stat(filepath.Join(rulesDir, saved.File)); err != nil {
		t.Fatalf("saved file not on disk: %v", err)
	}

	// The same file now loads — no restart.
	resp = mustPostJSON(t, srvHTTP.URL+"/api/files/load", map[string]string{"path": path})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the file still does not load after saving the rule: %d", resp.StatusCode)
	}

	// And the diagnosis now names the adapter that claimed it.
	resp2 := mustPostJSON(t, srvHTTP.URL+"/api/files/explain", map[string]string{"path": path})
	defer resp2.Body.Close()
	var d struct {
		Chosen string `json:"chosen"`
	}
	decode(t, resp2, &d)
	if d.Chosen != "line" {
		t.Errorf("chosen = %q after saving a line rule, want line", d.Chosen)
	}
}

// A name collision is the one failure the UI resolves by itself, so it
// needs a status to branch on rather than a string to match.
func TestSaveRuleConflictStatus(t *testing.T) {
	srvHTTP, _, _ := newRuleTestServer(t)

	resp := mustPostJSON(t, srvHTTP.URL+"/api/rules/suggest", map[string]string{"sample": gatewaySample})
	var draft map[string]any
	decode(t, resp, &draft)
	resp.Body.Close()
	draft["name"] = "dupe"

	resp = mustPostJSON(t, srvHTTP.URL+"/api/rules/save", map[string]any{"rule": draft})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first save returned %d", resp.StatusCode)
	}

	resp = mustPostJSON(t, srvHTTP.URL+"/api/rules/save", map[string]any{"rule": draft})
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second save returned %d, want 409", resp.StatusCode)
	}

	resp = mustPostJSON(t, srvHTTP.URL+"/api/rules/save", map[string]any{"rule": draft, "overwrite": true})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("overwrite returned %d, want 200", resp.StatusCode)
	}
}

// A regex that does not compile is an ordinary state while typing, so
// the preview reports it in-band rather than failing the request.
func TestPreviewReportsBadRegexInBand(t *testing.T) {
	srvHTTP, _, _ := newRuleTestServer(t)
	resp := mustPostJSON(t, srvHTTP.URL+"/api/rules/preview", map[string]any{
		"rule":   map[string]any{"parse": `^(?P<ts>\d+`, "ts_layout": "15:04:05"},
		"sample": "12:00:00 hello",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preview returned %d for a half-typed regex, want 200", resp.StatusCode)
	}
	var prev struct {
		Error string `json:"error"`
	}
	decode(t, resp, &prev)
	if prev.Error == "" {
		t.Error("expected an error message in the body")
	}
}

// The rules list is what the builder checks a name against.
func TestRulesListing(t *testing.T) {
	srvHTTP, _, _ := newRuleTestServer(t)

	resp, err := http.Get(srvHTTP.URL + "/api/rules")
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Rules []map[string]any `json:"rules"`
		Dir   string           `json:"dir"`
	}
	decode(t, resp, &listed)
	resp.Body.Close()
	if len(listed.Rules) != 0 {
		t.Errorf("expected no rules in a fresh directory, got %d", len(listed.Rules))
	}
	if listed.Dir == "" {
		t.Error("dir should name the rules directory")
	}

	resp = mustPostJSON(t, srvHTTP.URL+"/api/rules/suggest", map[string]string{"sample": gatewaySample})
	var draft map[string]any
	decode(t, resp, &draft)
	resp.Body.Close()
	draft["name"] = "listed-rule"
	resp = mustPostJSON(t, srvHTTP.URL+"/api/rules/save", map[string]any{"rule": draft})
	resp.Body.Close()

	resp, err = http.Get(srvHTTP.URL + "/api/rules")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	decode(t, resp, &listed)
	if len(listed.Rules) != 1 || listed.Rules[0]["name"] != "listed-rule" {
		t.Errorf("listing did not report the saved rule: %#v", listed.Rules)
	}
}

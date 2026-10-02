package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/labmk/kopusha/internal/config"
	"github.com/labmk/kopusha/internal/engine"
	"github.com/labmk/kopusha/internal/module"
	"github.com/labmk/kopusha/internal/parsers"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// boot mounts the module through the real registry, the way main.go
// does, so the test also covers the module contract.
func boot(t *testing.T, conf string) *http.ServeMux {
	t.Helper()
	p := filepath.Join(t.TempDir(), "kopusha_mcp.conf")
	if err := os.WriteFile(p, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	mgr := parsers.NewManager(filepath.Join(repoRoot(t), "parsers.d"), eng.SetLoaders)
	if _, err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	reg := module.NewRegistry(cfg, module.Deps{Engine: eng, Rules: mgr})
	reg.Add(New("test"))
	if err := reg.Boot(mux); err != nil {
		t.Fatal(err)
	}
	return mux
}

type rpcReply struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func post(t *testing.T, mux *http.ServeMux, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9200"+Endpoint, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Content-Type", "application/json")
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func call(t *testing.T, mux *http.ServeMux, method string, params any) rpcReply {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	rec := post(t, mux, string(b), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", method, rec.Code, rec.Body.String())
	}
	var r rpcReply
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("%s: decode: %v", method, err)
	}
	return r
}

type toolReply struct {
	IsError           bool            `json:"isError"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	Content           []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func callTool(t *testing.T, mux *http.ServeMux, name string, args any) toolReply {
	t.Helper()
	r := call(t, mux, "tools/call", map[string]any{"name": name, "arguments": args})
	if r.Error != nil {
		t.Fatalf("%s: rpc error %+v", name, r.Error)
	}
	var tr toolReply
	if err := json.Unmarshal(r.Result, &tr); err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestDisabledWithoutSection(t *testing.T) {
	mux := boot(t, "port = 9200\n")
	rec := post(t, mux, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, nil)
	if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), `"result"`) {
		t.Fatal("endpoint mounted without [mcp] section")
	}
}

func TestInitializeAndToolsList(t *testing.T) {
	mux := boot(t, "[mcp]\n")
	r := call(t, mux, "initialize", map[string]any{"protocolVersion": "2025-06-18"})
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct{ Name, Version string }
	}
	_ = json.Unmarshal(r.Result, &init)
	if init.ProtocolVersion != "2025-06-18" || init.ServerInfo.Name != "kopusha" || init.ServerInfo.Version != "test" {
		t.Fatalf("initialize = %s", r.Result)
	}
	r = call(t, mux, "initialize", map[string]any{"protocolVersion": "1999-01-01"})
	_ = json.Unmarshal(r.Result, &init)
	if init.ProtocolVersion != latestVersion {
		t.Fatalf("unknown version should fall back to %s, got %s", latestVersion, init.ProtocolVersion)
	}

	r = call(t, mux, "tools/list", nil)
	var list struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	_ = json.Unmarshal(r.Result, &list)
	var names []string
	for _, tl := range list.Tools {
		names = append(names, tl.Name)
	}
	want := "list_files,load_directory,list_formats,get_fields,field_samples,query,explain_detection"
	if strings.Join(names, ",") != want {
		t.Fatalf("tools = %v, want %s", names, want)
	}
}

func TestAllowLoadFalseHidesLoad(t *testing.T) {
	mux := boot(t, "[mcp]\nallow_load = false\n")
	r := call(t, mux, "tools/list", nil)
	if strings.Contains(string(r.Result), "load_directory") {
		t.Fatal("load_directory listed with allow_load = false")
	}
	r = call(t, mux, "tools/call", map[string]any{"name": "load_directory", "arguments": map[string]any{"path": "/"}})
	if r.Error == nil || r.Error.Code != codeInvalidParams {
		t.Fatalf("expected unknown-tool error, got %+v", r)
	}
}

func TestEndToEnd(t *testing.T) {
	mux := boot(t, "[mcp]\nmax_rows = 10\n")
	dir := filepath.Join(repoRoot(t), "test-fixtures", "ndjson")
	abs, _ := filepath.Abs(dir)

	tr := callTool(t, mux, "load_directory", map[string]any{"path": abs})
	if tr.IsError {
		t.Fatalf("load_directory: %s", tr.Content[0].Text)
	}
	var loaded struct{ Loaded, Errors []string }
	_ = json.Unmarshal(tr.StructuredContent, &loaded)
	if len(loaded.Loaded) != 2 {
		t.Fatalf("loaded = %+v", loaded)
	}

	tr = callTool(t, mux, "list_files", map[string]any{})
	if !strings.Contains(string(tr.StructuredContent), "gateway.ndjson") {
		t.Fatalf("list_files = %s", tr.StructuredContent)
	}

	tr = callTool(t, mux, "get_fields", nil)
	var fields struct{ Fields []string }
	_ = json.Unmarshal(tr.StructuredContent, &fields)
	if len(fields.Fields) == 0 {
		t.Fatal("get_fields returned nothing")
	}

	tr = callTool(t, mux, "field_samples", map[string]any{"fields": []string{"_source_format"}})
	if tr.IsError || !strings.Contains(string(tr.StructuredContent), "samples") {
		t.Fatalf("field_samples = %s", tr.StructuredContent)
	}

	tr = callTool(t, mux, "query", map[string]any{"limit": 5})
	var q struct {
		Rows       []map[string]any `json:"rows"`
		TotalCount int64            `json:"total_count"`
	}
	_ = json.Unmarshal(tr.StructuredContent, &q)
	if tr.IsError || len(q.Rows) != 5 || q.TotalCount < 5 {
		t.Fatalf("query: rows=%d total=%d err=%v", len(q.Rows), q.TotalCount, tr.IsError)
	}

	tr = callTool(t, mux, "query", map[string]any{"limit": 11})
	if !tr.IsError || !strings.Contains(tr.Content[0].Text, "max_rows") {
		t.Fatalf("limit over max_rows should be a tool error, got %+v", tr)
	}

	tr = callTool(t, mux, "query", map[string]any{"bogus": 1})
	if !tr.IsError {
		t.Fatal("unknown argument should be a tool error")
	}

	tr = callTool(t, mux, "list_formats", nil)
	if tr.IsError || !strings.Contains(string(tr.StructuredContent), "ndjson") {
		t.Fatalf("list_formats = %s", tr.StructuredContent)
	}

	fixture, _ := filepath.Abs(filepath.Join(repoRoot(t), "test-fixtures", "formats", "line-iso-bracket.log"))
	tr = callTool(t, mux, "explain_detection", map[string]any{"path": fixture})
	var d struct {
		Chosen string `json:"chosen"`
	}
	_ = json.Unmarshal(tr.StructuredContent, &d)
	if tr.IsError || d.Chosen == "" {
		t.Fatalf("explain_detection = %s", tr.StructuredContent)
	}
}

func TestZipRefused(t *testing.T) {
	mux := boot(t, "[mcp]\n")
	p := filepath.Join(t.TempDir(), "logs.zip")
	if err := os.WriteFile(p, []byte("PK"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := callTool(t, mux, "load_directory", map[string]any{"path": p})
	if !tr.IsError || !strings.Contains(tr.Content[0].Text, "zip") {
		t.Fatalf("zip load should be refused, got %+v", tr)
	}
}

func TestLocalOnly(t *testing.T) {
	mux := boot(t, "[mcp]\n")
	body := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	cases := []struct {
		name string
		mod  func(*http.Request)
		want int
	}{
		{"remote address", func(r *http.Request) { r.RemoteAddr = "192.168.1.5:4000" }, http.StatusForbidden},
		{"rebinding host", func(r *http.Request) { r.Host = "evil.example:9200" }, http.StatusForbidden},
		{"foreign origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, http.StatusForbidden},
		{"local origin", func(r *http.Request) { r.Header.Set("Origin", "http://localhost:9200") }, http.StatusOK},
		{"ipv6 loopback", func(r *http.Request) { r.RemoteAddr = "[::1]:4000"; r.Host = "[::1]:9200" }, http.StatusOK},
		{"bad protocol header", func(r *http.Request) { r.Header.Set("MCP-Protocol-Version", "1999-01-01") }, http.StatusBadRequest},
	}
	for _, c := range cases {
		if got := post(t, mux, body, c.mod).Code; got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}
}

func TestToken(t *testing.T) {
	mux := boot(t, "[mcp]\ntoken = s3cret\n")
	body := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	if got := post(t, mux, body, nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("no token: %d", got)
	}
	ok := post(t, mux, body, func(r *http.Request) { r.Header.Set("Authorization", "Bearer s3cret") })
	if ok.Code != http.StatusOK {
		t.Fatalf("with token: %d", ok.Code)
	}
}

func TestProtocolEdges(t *testing.T) {
	mux := boot(t, "[mcp]\n")
	if got := post(t, mux, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil); got.Code != http.StatusAccepted {
		t.Fatalf("notification: %d", got.Code)
	}
	rec := post(t, mux, `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, nil)
	if !strings.Contains(rec.Body.String(), "batch") {
		t.Fatalf("batch: %s", rec.Body.String())
	}
	rec = post(t, mux, `{not json`, nil)
	if !strings.Contains(rec.Body.String(), "-32700") {
		t.Fatalf("parse error: %s", rec.Body.String())
	}
	r := call(t, mux, "resources/list", nil)
	if r.Error == nil || r.Error.Code != codeMethodNotFound {
		t.Fatalf("unknown method: %+v", r)
	}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9200"+Endpoint, nil)
	req.RemoteAddr = "127.0.0.1:1"
	get := httptest.NewRecorder()
	mux.ServeHTTP(get, req)
	if get.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", get.Code)
	}

	big := bytes.Repeat([]byte("x"), maxBody+10)
	rec = post(t, mux, string(big), nil)
	if rec.Code != http.StatusRequestEntityTooLarge {
		b, _ := io.ReadAll(rec.Body)
		t.Fatalf("oversized body: %d %s", rec.Code, b)
	}
}

func TestParseOptions(t *testing.T) {
	if _, err := parseOptions(map[string]string{"max_rows": "0"}); err == nil {
		t.Error("max_rows = 0 accepted")
	}
	if _, err := parseOptions(map[string]string{"allow_load": "maybe"}); err == nil {
		t.Error("allow_load = maybe accepted")
	}
	o, err := parseOptions(map[string]string{})
	if err != nil || o.MaxRows != defaultMaxRows || !o.AllowLoad || o.Token != "" {
		t.Errorf("defaults = %+v, %v", o, err)
	}
}

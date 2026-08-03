package module

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labmk/kopusha/internal/config"
)

// --- helpers ---

func loadConf(t *testing.T, body string) *config.Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "kopusha.conf")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("load conf: %v", err)
	}
	return cfg
}

// fakeModule is a configurable test double for Module.
type fakeModule struct {
	name        string
	enabled     bool
	registered  bool
	registerErr error
	// Optional manifest fillers.
	tab    *TabEntry
	bundle string
	style  string
	cfg    map[string]any
	// Optional extra route the module mounts.
	mountPath string
	mountResp string
}

func (m *fakeModule) Name() string                  { return m.name }
func (m *fakeModule) Enabled(_ *config.Config) bool { return m.enabled }
func (m *fakeModule) Register(ctx *RegisterContext) error {
	m.registered = true
	if m.registerErr != nil {
		return m.registerErr
	}
	if m.tab != nil {
		ctx.Manifest.Tab = m.tab
	}
	if m.bundle != "" {
		ctx.Manifest.Bundle = m.bundle
	}
	if m.style != "" {
		ctx.Manifest.Style = m.style
	}
	if m.cfg != nil {
		ctx.Manifest.Config = m.cfg
	}
	if m.mountPath != "" {
		resp := m.mountResp
		ctx.Mux.HandleFunc(m.mountPath, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(resp))
		})
	}
	return nil
}

// --- tests ---

func TestRegistry_BootEmpty_ExposesEmptyList(t *testing.T) {
	r := NewRegistry(loadConf(t, ""), Deps{})
	mux := http.NewServeMux()
	if err := r.Boot(mux); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/modules", nil))

	var got struct {
		Modules []*Manifest `json:"modules"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Modules) != 0 {
		t.Errorf("empty registry should produce empty list, got %d", len(got.Modules))
	}
	// Verify the JSON is "[]" not "null".
	if !strings.Contains(rec.Body.String(), "[]") && strings.Contains(rec.Body.String(), "null") {
		t.Errorf("response should serialize empty list as [], got: %s", rec.Body.String())
	}
}

func TestRegistry_DisabledModule_NotMounted(t *testing.T) {
	r := NewRegistry(loadConf(t, ""), Deps{})
	m := &fakeModule{
		name:      "off",
		enabled:   false,
		mountPath: "/api/off/ping",
		mountResp: "pong",
	}
	r.Add(m)
	mux := http.NewServeMux()
	if err := r.Boot(mux); err != nil {
		t.Fatal(err)
	}
	if m.registered {
		t.Error("disabled module: Register should NOT have been called")
	}
	if len(r.Manifests()) != 0 {
		t.Errorf("disabled module: should not appear in manifests, got %v", r.Manifests())
	}
	// Route should not exist.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/off/ping", nil))
	if rec.Code == http.StatusOK {
		t.Errorf("disabled module's route should not be mounted, got 200")
	}
}

func TestRegistry_EnabledModule_RoutesAndManifest(t *testing.T) {
	r := NewRegistry(loadConf(t, ""), Deps{})
	m := &fakeModule{
		name:      "alpha",
		enabled:   true,
		tab:       &TabEntry{Label: "Alpha", Route: "/m/alpha"},
		bundle:    "/m/alpha/index.js",
		mountPath: "/api/alpha/ping",
		mountResp: "pong",
	}
	r.Add(m)
	mux := http.NewServeMux()
	if err := r.Boot(mux); err != nil {
		t.Fatal(err)
	}
	if !m.registered {
		t.Fatal("enabled module: Register should have been called")
	}

	// Manifest surfaces in /api/modules.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/modules", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `"id":"alpha"`) {
		t.Errorf("manifest missing id: %s", body)
	}
	if !strings.Contains(body, `"label":"Alpha"`) {
		t.Errorf("manifest missing tab: %s", body)
	}
	if !strings.Contains(body, `"bundle":"/m/alpha/index.js"`) {
		t.Errorf("manifest missing bundle: %s", body)
	}

	// Mounted route is reachable.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/alpha/ping", nil))
	if rec.Body.String() != "pong" {
		t.Errorf("module route not mounted: got %q", rec.Body.String())
	}
}

func TestRegistry_AddOrderPreserved(t *testing.T) {
	r := NewRegistry(loadConf(t, ""), Deps{})
	r.Add(&fakeModule{name: "first", enabled: true})
	r.Add(&fakeModule{name: "second", enabled: true})
	r.Add(&fakeModule{name: "third", enabled: true})

	mux := http.NewServeMux()
	if err := r.Boot(mux); err != nil {
		t.Fatal(err)
	}

	mfs := r.Manifests()
	if len(mfs) != 3 {
		t.Fatalf("expected 3 manifests, got %d", len(mfs))
	}
	want := []string{"first", "second", "third"}
	for i, m := range mfs {
		if m.ID != want[i] {
			t.Errorf("manifest[%d]: want %q, got %q", i, want[i], m.ID)
		}
	}
}

func TestRegistry_OmitemptyOnManifest(t *testing.T) {
	// A module that only fills Config (a theme, say) should NOT emit
	// tab/bundle/style fields in the JSON.
	r := NewRegistry(loadConf(t, ""), Deps{})
	r.Add(&fakeModule{
		name:    "theme",
		enabled: true,
		cfg:     map[string]any{"company": "Acme"},
	})
	mux := http.NewServeMux()
	if err := r.Boot(mux); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/modules", nil))
	body := rec.Body.String()
	for _, banned := range []string{`"tab":`, `"bundle":`, `"style":`} {
		if strings.Contains(body, banned) {
			t.Errorf("omitempty should strip %s for data-only module, got: %s", banned, body)
		}
	}
	if !strings.Contains(body, `"config":{"company":"Acme"}`) {
		t.Errorf("config payload missing: %s", body)
	}
}

func TestRegistry_RegisterError_Propagates(t *testing.T) {
	r := NewRegistry(loadConf(t, ""), Deps{})
	r.Add(&fakeModule{
		name:        "broken",
		enabled:     true,
		registerErr: errors.New("bad config"),
	})
	err := r.Boot(http.NewServeMux())
	if err == nil {
		t.Fatal("expected Boot error from failing Register")
	}
	if !strings.Contains(err.Error(), "broken") || !strings.Contains(err.Error(), "bad config") {
		t.Errorf("error should name the module and underlying cause, got: %v", err)
	}
}

func TestRegistry_DoubleBoot_Errors(t *testing.T) {
	r := NewRegistry(loadConf(t, ""), Deps{})
	if err := r.Boot(http.NewServeMux()); err != nil {
		t.Fatal(err)
	}
	if err := r.Boot(http.NewServeMux()); err == nil {
		t.Error("second Boot should error")
	}
}

func TestRegistry_AddAfterBoot_Ignored(t *testing.T) {
	r := NewRegistry(loadConf(t, ""), Deps{})
	if err := r.Boot(http.NewServeMux()); err != nil {
		t.Fatal(err)
	}
	r.Add(&fakeModule{name: "late", enabled: true})
	if got := len(r.Manifests()); got != 0 {
		t.Errorf("late Add should be ignored, got %d manifests", got)
	}
}

func TestRegistry_ModulesEndpointMethodNotAllowed(t *testing.T) {
	r := NewRegistry(loadConf(t, ""), Deps{})
	mux := http.NewServeMux()
	if err := r.Boot(mux); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/modules", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST should be 405, got %d", rec.Code)
	}
}

func TestRegistry_ConfDrivenEnabled(t *testing.T) {
	// Module that defers to cfg.ModuleEnabled (the real-world pattern).
	type confDriven struct{ name string }
	r := NewRegistry(loadConf(t, "[collector]\nenabled = true\n"), Deps{})
	m := &realisticModule{
		name: "collector",
		check: func(cfg *config.Config) bool {
			return cfg.ModuleEnabled("collector")
		},
	}
	r.Add(m)
	if err := r.Boot(http.NewServeMux()); err != nil {
		t.Fatal(err)
	}
	if len(r.Manifests()) != 1 {
		t.Errorf("conf-enabled module should mount, got %d manifests", len(r.Manifests()))
	}

	// Same module, missing section → not enabled.
	r2 := NewRegistry(loadConf(t, "port = 9200\n"), Deps{})
	r2.Add(&realisticModule{
		name:  "collector",
		check: func(cfg *config.Config) bool { return cfg.ModuleEnabled("collector") },
	})
	if err := r2.Boot(http.NewServeMux()); err != nil {
		t.Fatal(err)
	}
	if len(r2.Manifests()) != 0 {
		t.Errorf("missing section should disable module, got %d manifests", len(r2.Manifests()))
	}
}

// realisticModule lets a test drive Enabled() with a closure.
type realisticModule struct {
	name  string
	check func(*config.Config) bool
}

func (m *realisticModule) Name() string                      { return m.name }
func (m *realisticModule) Enabled(c *config.Config) bool     { return m.check(c) }
func (m *realisticModule) Register(_ *RegisterContext) error { return nil }

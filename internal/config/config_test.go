package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConf(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "obs_viewer.conf")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestLoad_MissingFile(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.conf"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if cfg.FlatKeys() != 0 || len(cfg.Sections()) != 0 {
		t.Fatalf("missing file should produce empty config, got %d flat, %d sections",
			cfg.FlatKeys(), len(cfg.Sections()))
	}
}

func TestLoad_FlatKeysCompat(t *testing.T) {
	// Pre-0.4 obs_viewer.conf — no sections, just three flat keys.
	p := writeConf(t, "port = 9201\ntimeout = 600\nlisten=0.0.0.0\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.GetInt("port", 9200); got != 9201 {
		t.Errorf("port: want 9201, got %d", got)
	}
	if got := cfg.GetInt("timeout", 10600); got != 600 {
		t.Errorf("timeout: want 600, got %d", got)
	}
	if got := cfg.GetDefault("listen", "127.0.0.1"); got != "0.0.0.0" {
		t.Errorf("listen: want 0.0.0.0, got %q", got)
	}
	if len(cfg.Sections()) != 0 {
		t.Errorf("flat-only conf should have no sections, got %v", cfg.Sections())
	}
}

func TestLoad_FlatDefaults(t *testing.T) {
	cfg, _ := Load(writeConf(t, ""))
	if cfg.GetInt("port", 9200) != 9200 {
		t.Error("port default")
	}
	if cfg.GetDefault("listen", "127.0.0.1") != "127.0.0.1" {
		t.Error("listen default")
	}
	if cfg.GetInt("port", 9200) != 9200 {
		t.Error("default fallback on missing key")
	}
}

func TestLoad_GetIntInvalid(t *testing.T) {
	cfg, _ := Load(writeConf(t, "port = nine-thousand\n"))
	if got := cfg.GetInt("port", 9200); got != 9200 {
		t.Errorf("invalid int should fall back to default, got %d", got)
	}
}

func TestLoad_CommentsAndBlanks(t *testing.T) {
	body := `
# leading comment
; semicolon comment also ignored

port = 9210

# blank line above me, full-line comment below me
; one more
timeout=42
`
	cfg, _ := Load(writeConf(t, body))
	if cfg.GetInt("port", 0) != 9210 {
		t.Errorf("port: got %d", cfg.GetInt("port", 0))
	}
	if cfg.GetInt("timeout", 0) != 42 {
		t.Errorf("timeout: got %d", cfg.GetInt("timeout", 0))
	}
}

func TestLoad_Sections(t *testing.T) {
	body := `
port = 9200

[collector]
remote_host = collector.example.com
remote_port = 9200
ca_cert = /etc/ssl/certs/ca.crt

[theme]
company = Example Corp
link = https://example.com/
primary_color = #009999
`
	cfg, err := Load(writeConf(t, body))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.GetInt("port", 0) != 9200 {
		t.Errorf("flat port lost: %d", cfg.GetInt("port", 0))
	}

	ox, ok := cfg.Section("collector")
	if !ok {
		t.Fatal("collector section missing")
	}
	if ox["remote_host"] != "collector.example.com" {
		t.Errorf("remote_host: %q", ox["remote_host"])
	}
	if ox["remote_port"] != "9200" {
		t.Errorf("remote_port: %q", ox["remote_port"])
	}
	if ox["ca_cert"] != "/etc/ssl/certs/ca.crt" {
		t.Errorf("ca_cert: %q", ox["ca_cert"])
	}

	br, ok := cfg.Section("theme")
	if !ok {
		t.Fatal("theme section missing")
	}
	if br["company"] != "Example Corp" {
		t.Errorf("company: %q", br["company"])
	}
	if br["primary_color"] != "#009999" {
		t.Errorf("primary_color: %q", br["primary_color"])
	}
}

func TestLoad_DuplicateKeysLastWins(t *testing.T) {
	cfg, _ := Load(writeConf(t, "port = 1\nport = 2\n"))
	if cfg.GetInt("port", 0) != 2 {
		t.Errorf("duplicate key: last should win, got %d", cfg.GetInt("port", 0))
	}
}

func TestLoad_DuplicateSectionsMerge(t *testing.T) {
	body := `
[collector]
remote_host = a

[theme]
company = X

[collector]
remote_port = 9200
`
	cfg, _ := Load(writeConf(t, body))
	ox, _ := cfg.Section("collector")
	if ox["remote_host"] != "a" || ox["remote_port"] != "9200" {
		t.Errorf("duplicate section should merge, got %+v", ox)
	}
}

func TestLoad_MalformedSectionHeader(t *testing.T) {
	// "[oops" missing closing bracket — should NOT be treated as a section.
	// The line has no '=' either, so it's silently ignored.
	body := `
port = 9200

[oops

[theme]
company = Y
`
	cfg, _ := Load(writeConf(t, body))
	if cfg.HasSection("oops") {
		t.Error("malformed header should not register a section")
	}
	br, _ := cfg.Section("theme")
	if br["company"] != "Y" {
		t.Errorf("subsequent section should still parse, got %+v", br)
	}
}

func TestLoad_KeyBeforeAndAfterSection(t *testing.T) {
	body := `
port = 9200

[theme]
company = X

# Keys after a section header belong to that section, not the flat map.
also_theme = yes
`
	cfg, _ := Load(writeConf(t, body))
	if cfg.GetInt("port", 0) != 9200 {
		t.Error("pre-section flat key lost")
	}
	br, _ := cfg.Section("theme")
	if br["also_theme"] != "yes" {
		t.Errorf("post-header key should belong to section, got %+v", br)
	}
	if cfg.Get("also_theme") != "" {
		t.Error("post-header key leaked into flat map")
	}
}

func TestModuleEnabled(t *testing.T) {
	body := `
[present_default]
key = value

[explicit_true]
enabled = true

[explicit_false]
enabled = false

[empty_value]
enabled =

[zero]
enabled = 0

[upper_false]
enabled = FALSE

[no]
enabled = no
`
	cfg, _ := Load(writeConf(t, body))

	cases := map[string]bool{
		"missing":         false, // section not present
		"present_default": true,  // present, no "enabled" key
		"explicit_true":   true,
		"explicit_false":  false,
		"empty_value":     false,
		"zero":            false,
		"upper_false":     false,
		"no":              false,
	}
	for section, want := range cases {
		if got := cfg.ModuleEnabled(section); got != want {
			t.Errorf("ModuleEnabled(%q) = %v, want %v", section, got, want)
		}
	}
}

func TestLoad_WhitespaceTolerance(t *testing.T) {
	body := "   port   =    9201   \n   [  theme  ]   \n   company  =   Acme  \n"
	cfg, _ := Load(writeConf(t, body))
	if cfg.GetInt("port", 0) != 9201 {
		t.Errorf("whitespace around flat key/value: got %d", cfg.GetInt("port", 0))
	}
	br, ok := cfg.Section("theme")
	if !ok {
		t.Fatal("whitespace-padded section header should still parse")
	}
	if br["company"] != "Acme" {
		t.Errorf("whitespace around section key/value: got %q", br["company"])
	}
}

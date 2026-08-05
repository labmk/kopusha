package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func decodeSamples(t *testing.T, s *Server) SamplesResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleSamples(rec, httptest.NewRequest(http.MethodGet, "/api/samples", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got SamplesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSamplesListsShippedLogs(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"sample.ndjson", "line-iso-bracket.log", "unmatched.log", "README.md"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := &Server{samplesDir: dir}
	got := decodeSamples(t, s)

	if !got.Available {
		t.Fatal("available = false")
	}
	names := make([]string, len(got.Files))
	for i, f := range got.Files {
		names[i] = f.Name
	}
	// README is for a person, not the engine; directories are not logs.
	want := []string{"line-iso-bracket.log", "sample.ndjson", "unmatched.log"}
	if len(names) != len(want) {
		t.Fatalf("files = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("files = %v, want %v (sorted)", names, want)
		}
	}
	// Paths must be absolute enough for /api/files/load to use directly.
	for _, f := range got.Files {
		if !filepath.IsAbs(f.Path) {
			t.Errorf("%s: path %q is not absolute", f.Name, f.Path)
		}
	}
}

// A binary copied on its own has no samples/. That is a state the UI
// renders nothing for, not an error.
func TestSamplesReportsAbsentFolder(t *testing.T) {
	for _, tc := range []struct {
		name string
		dir  string
	}{
		{"unset", ""},
		{"missing", filepath.Join(t.TempDir(), "not-created")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeSamples(t, &Server{samplesDir: tc.dir})
			if got.Available {
				t.Error("available = true")
			}
			if len(got.Files) != 0 {
				t.Errorf("files = %v, want none", got.Files)
			}
			if got.Dir != "" {
				t.Errorf("dir = %q, want empty", got.Dir)
			}
		})
	}
}

// A folder holding only the README offers nothing to load.
func TestSamplesWithOnlyTheReadmeIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := decodeSamples(t, &Server{samplesDir: dir}); got.Available {
		t.Errorf("available = true with only a README: %+v", got)
	}
}

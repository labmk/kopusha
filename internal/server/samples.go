package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// samplesDoc is shipped for a person to read, not for the engine to
// parse, so it is left out of the list the UI offers to load.
const samplesDoc = "README.md"

// SampleFile is one shipped sample log.
type SampleFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// SamplesResponse lists the samples/ folder beside the binary.
type SamplesResponse struct {
	// Available is false when the folder is absent — a binary copied on
	// its own, or an install that predates samples/. The UI offers
	// nothing rather than a button that fails.
	Available bool         `json:"available"`
	Dir       string       `json:"dir,omitempty"`
	Files     []SampleFile `json:"files"`
}

// SetSamplesDir tells the server where the shipped samples live. Empty
// leaves the feature switched off.
func (s *Server) SetSamplesDir(dir string) { s.samplesDir = dir }

// handleSamples lists the shipped sample logs.
//
// It exists because the frontend cannot know where the binary lives, and
// because 0.3.1 shipped these files with nothing in the interface
// pointing at them. The hardest moment for a new user is having no data
// to look at; this is the one click that fixes it.
//
// @Summary      List shipped sample logs
// @Description  Sample files in samples/ beside the binary, for the empty state's "Try the samples" action.
// @Tags         files
// @Produce      json
// @Success      200  {object}  SamplesResponse
// @Router       /api/samples [get]
func (s *Server) handleSamples(w http.ResponseWriter, r *http.Request) {
	resp := SamplesResponse{Files: []SampleFile{}}
	if s.samplesDir == "" {
		writeJSON(w, resp)
		return
	}
	entries, err := os.ReadDir(s.samplesDir)
	if err != nil {
		// A missing folder is a state, not a failure: it is what a
		// copied binary looks like.
		writeJSON(w, resp)
		return
	}
	for _, e := range entries {
		if e.IsDir() || strings.EqualFold(e.Name(), samplesDoc) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		resp.Files = append(resp.Files, SampleFile{
			Name: e.Name(),
			Path: filepath.Join(s.samplesDir, e.Name()),
			Size: info.Size(),
		})
	}
	sort.Slice(resp.Files, func(i, j int) bool { return resp.Files[i].Name < resp.Files[j].Name })
	resp.Available = len(resp.Files) > 0
	if resp.Available {
		resp.Dir = s.samplesDir
	}
	writeJSON(w, resp)
}

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"github.com/labmk/obs-viewer/internal/ingest"
	"github.com/labmk/obs-viewer/internal/logx"
	"github.com/labmk/obs-viewer/internal/parsers"
)

// SetRules attaches the parsers.d manager. Called once from main after
// New, like SetUpdateChecker: the rules directory is resolved from
// config, which the server constructor does not see.
//
// Without it the rule endpoints report 503 rather than 404. A missing
// capability and a missing route are different problems, and the UI
// hides the rule builder for the first but should complain loudly about
// the second.
func (s *Server) SetRules(m *parsers.Manager) { s.rules = m }

func (s *Server) registerRuleRoutes() {
	s.mux.HandleFunc("/api/files/explain", s.APIHandler(s.handleExplainFile))
	s.mux.HandleFunc("/api/rules", s.APIHandler(s.handleRules))
	s.mux.HandleFunc("/api/rules/suggest", s.APIHandler(s.handleRuleSuggest))
	s.mux.HandleFunc("/api/rules/preview", s.APIHandler(s.handleRulePreview))
	s.mux.HandleFunc("/api/rules/save", s.APIHandler(s.handleRuleSave))
}

// rulesReady reports whether the manager is attached, writing the error
// itself when it is not.
func (s *Server) rulesReady(w http.ResponseWriter) bool {
	if s.rules == nil {
		writeError(w, "rule management is not available in this build", http.StatusServiceUnavailable)
		return false
	}
	return true
}

// @Summary      Explain how a file was, or was not, matched
// @Description  Runs every ingest adapter's detection against the file and reports each one's score and reason, the first non-blank line as the parser sees it, and any encoding trait that commonly breaks matching (BOM, CRLF, NUL bytes, invalid UTF-8). Read-only: nothing is loaded. This is what the UI shows when a load fails, and it is the input to the rule builder.
// @Tags         files
// @Accept       json
// @Produce      json
// @Param        body  body      PathRequest      true  "path to diagnose"
// @Success      200   {object}  DiagnosisResponse
// @Failure      400   {object}  ErrorResponse
// @Router       /api/files/explain [post]
func (s *Server) handleExplainFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !s.rulesReady(w) {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Path == "" {
		writeError(w, "Path is required", http.StatusBadRequest)
		return
	}

	// A zip entry is diagnosed as the extracted file, because that is
	// what the adapters would actually have seen.
	path := req.Path
	if isZipVirtPath(path) {
		zipPath, inner := splitZipVirt(path)
		extracted, err := extractZipEntry(zipPath, inner)
		if err != nil {
			writeError(w, "extract: "+err.Error(), http.StatusBadRequest)
			return
		}
		path = extracted
	}

	hint, err := ingest.HintForFile(path)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, s.rules.Registry().Explain(hint))
}

// @Summary      List parser rules
// @Description  Every rule currently loaded from parsers.d, with the file it came from. Used by the rule builder to warn before a name collides with an existing rule.
// @Tags         rules
// @Produce      json
// @Success      200  {object}  RulesResponse
// @Router       /api/rules [get]
func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	if !s.rulesReady(w) {
		return
	}
	list, err := s.rules.List()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []parsers.Info{}
	}
	writeJSON(w, map[string]interface{}{
		"rules": list,
		"dir":   s.rules.Dir(),
	})
}

// @Summary      Infer a rule from sample lines
// @Description  Derives a candidate line rule — regex with named captures, plus a Go time layout — from pasted sample lines. Nothing is written; the result is a starting point for the builder, meant to be corrected against the preview.
// @Tags         rules
// @Accept       json
// @Produce      json
// @Param        body  body      SampleRequest    true  "sample text"
// @Success      200   {object}  RuleDraft
// @Failure      400   {object}  ErrorResponse
// @Router       /api/rules/suggest [post]
func (s *Server) handleRuleSuggest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Sample string `json:"sample"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	writeJSON(w, parsers.Suggest(req.Sample))
}

// @Summary      Preview what a rule would produce
// @Description  Runs a candidate rule over sample text through the real line adapter and returns the rows, the per-line verdict (parsed or folded into the previous record as a continuation), and any timestamp that the layout rejected. Nothing is written.
// @Tags         rules
// @Accept       json
// @Produce      json
// @Param        body  body      PreviewRequest   true  "draft rule + sample"
// @Success      200   {object}  PreviewResponse
// @Failure      400   {object}  ErrorResponse
// @Router       /api/rules/preview [post]
func (s *Server) handleRulePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Rule   parsers.Draft `json:"rule"`
		Sample string        `json:"sample"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	// A rule that does not compile is an expected state while typing,
	// not a request error: the preview reports it in-band so the editor
	// can show it under the field without the UI treating it as a
	// failed request.
	writeJSON(w, parsers.PreviewDraft(r.Context(), req.Rule, sampleOr(req.Sample, req.Rule.Sample)))
}

func sampleOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// @Summary      Save a rule to parsers.d
// @Description  Writes the rule as a YAML file next to the binary and reloads the parser registry, so it applies to files loaded afterwards without a restart. The rule name is normalized into a filename; a rule that fails to compile is not left on disk. Existing files are only replaced when overwrite is set.
// @Tags         rules
// @Accept       json
// @Produce      json
// @Param        body  body      SaveRuleRequest  true  "draft rule"
// @Success      200   {object}  SaveRuleResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse
// @Router       /api/rules/save [post]
func (s *Server) handleRuleSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !s.rulesReady(w) {
		return
	}
	var req struct {
		Rule      parsers.Draft `json:"rule"`
		Overwrite bool          `json:"overwrite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	path, err := s.rules.Save(req.Rule, req.Overwrite)
	if err != nil {
		// A name collision is the one failure the UI can resolve by
		// itself, by asking whether to replace — so it gets a status the
		// client can branch on rather than a string to match.
		code := http.StatusBadRequest
		if errors.Is(err, parsers.ErrRuleExists) {
			code = http.StatusConflict
		}
		writeError(w, err.Error(), code)
		return
	}

	stats := s.rules.Stats()
	logx.Info("rules.saved", logx.F{
		"path":  path,
		"name":  req.Rule.Name,
		"rules": stats.Line,
	})
	writeJSON(w, map[string]interface{}{
		"status": "ok",
		"path":   path,
		"file":   baseName(path),
		"rules":  stats.Line,
	})
}

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if os.IsPathSeparator(p[i]) {
			return p[i+1:]
		}
	}
	return p
}

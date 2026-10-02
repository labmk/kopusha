package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labmk/kopusha/internal/engine"
	"github.com/labmk/kopusha/internal/ingest"
)

// tool is one MCP tool: a name, a JSON Schema for its arguments, and
// the function that runs it. Run returns any JSON-marshalable value,
// which goes back as both text and structuredContent.
type tool struct {
	Name        string
	Description string
	Schema      map[string]any
	ReadOnly    bool
	Run         func(r *http.Request, args json.RawMessage) (any, error)
}

func (h *handler) findTool(name string) *tool {
	for i := range h.tools {
		if h.tools[i].Name == name {
			return &h.tools[i]
		}
	}
	return nil
}

func objectSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func (h *handler) buildTools() []tool {
	tools := []tool{
		{
			Name:        "list_files",
			Description: "Files currently loaded into the session, with record counts and whether each participates in queries.",
			Schema:      objectSchema(map[string]any{}),
			ReadOnly:    true,
			Run:         h.listFiles,
		},
	}
	if h.opts.AllowLoad {
		tools = append(tools, tool{
			Name: "load_directory",
			Description: "Load every supported file in a directory (not recursive), or a single file, into the session. " +
				"Each file is format-detected; files no adapter accepts are reported in errors, not fatal. " +
				"Nothing is written next to the input. Zip archives are refused.",
			Schema: objectSchema(map[string]any{
				"path": map[string]any{"type": "string", "description": "Absolute path to a directory or file on this machine."},
			}, "path"),
			Run: h.loadDirectory,
		})
	}
	tools = append(tools,
		tool{
			Name:        "list_formats",
			Description: "The ingest adapters this build has and the parser rules loaded from parsers.d, i.e. what can be parsed.",
			Schema:      objectSchema(map[string]any{}),
			ReadOnly:    true,
			Run:         h.listFormats,
		},
		tool{
			Name: "get_fields",
			Description: "Union of field names across all enabled files, including dotted STRUCT sub-paths " +
				"(e.g. agent.id). These are the names query filters and field_samples accept.",
			Schema:   objectSchema(map[string]any{}),
			ReadOnly: true,
			Run:      h.getFields,
		},
		tool{
			Name: "field_samples",
			Description: "Distinct values per field, so filters use real values instead of guesses. " +
				"A field with more distinct values than cap comes back as an empty list (high cardinality); " +
				"a field present in no file is omitted.",
			Schema: objectSchema(map[string]any{
				"fields": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1},
				"cap":    map[string]any{"type": "integer", "minimum": 1, "maximum": maxSampleCap, "default": defaultSampleCap},
			}, "fields"),
			ReadOnly: true,
			Run:      h.fieldSamples,
		},
		tool{
			Name: "query",
			Description: "Rows matching a filter set across all enabled files, sorted by the detected timestamp. " +
				"Same filter model as the viewer. Operators: is, is_not, contains, not_contains, wildcard, " +
				"not_wildcard, exists, does_not_exist. logic (and|or) joins a filter to the next one.",
			Schema: objectSchema(map[string]any{
				"filters": map[string]any{
					"type": "array",
					"items": objectSchema(map[string]any{
						"field": map[string]any{"type": "string"},
						"operator": map[string]any{"type": "string", "enum": []string{
							"is", "is_not", "contains", "not_contains", "wildcard", "not_wildcard", "exists", "does_not_exist",
						}},
						"value": map[string]any{"type": "string"},
						"logic": map[string]any{"type": "string", "enum": []string{"and", "or"}},
					}, "field", "operator"),
				},
				"search_text": map[string]any{"type": "string", "description": "Free text matched across all fields."},
				"time_from":   map[string]any{"type": "string", "description": "Inclusive lower bound, ISO 8601."},
				"time_to":     map[string]any{"type": "string", "description": "Inclusive upper bound, ISO 8601."},
				"sort_order":  map[string]any{"type": "string", "enum": []string{"asc", "desc"}, "default": "desc"},
				"sort_field":  map[string]any{"type": "string"},
				"offset":      map[string]any{"type": "integer", "minimum": 0, "default": 0},
				"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": h.opts.MaxRows, "default": defaultQueryRows},
			}),
			ReadOnly: true,
			Run:      h.query,
		},
		tool{
			Name: "explain_detection",
			Description: "Why a file parses the way it does: every adapter's score and reason, the first line as the " +
				"parser sees it, and encoding traits that break matching (BOM, CRLF, NUL bytes, invalid UTF-8). " +
				"Does not load the file.",
			Schema: objectSchema(map[string]any{
				"path": map[string]any{"type": "string", "description": "Absolute path to a file on this machine."},
			}, "path"),
			ReadOnly: true,
			Run:      h.explainDetection,
		},
	)
	return tools
}

func decode(args json.RawMessage, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func (h *handler) listFiles(_ *http.Request, _ json.RawMessage) (any, error) {
	files := h.eng.GetFiles()
	if files == nil {
		files = []*engine.FileInfo{}
	}
	return map[string]any{"files": files}, nil
}

var errZip = errors.New("zip archives are not loaded over MCP: loading one extracts it to disk")

func isZip(p string) bool { return strings.EqualFold(filepath.Ext(p), ".zip") }

func (h *handler) loadDirectory(r *http.Request, args json.RawMessage) (any, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		return nil, errors.New("path is required")
	}
	if !filepath.IsAbs(a.Path) {
		return nil, errors.New("path must be absolute")
	}
	st, err := os.Stat(a.Path)
	if err != nil {
		return nil, err
	}

	loaded := []string{}
	failed := []string{}
	load := func(p string) {
		if isZip(p) {
			failed = append(failed, fmt.Sprintf("%s: %v", filepath.Base(p), errZip))
			return
		}
		if err := h.eng.LoadFileCtx(r.Context(), p); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", filepath.Base(p), err))
			return
		}
		loaded = append(loaded, filepath.Base(p))
	}

	if !st.IsDir() {
		if isZip(a.Path) {
			return nil, errZip
		}
		load(a.Path)
	} else {
		entries, err := os.ReadDir(a.Path)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if err := r.Context().Err(); err != nil {
				failed = append(failed, "cancelled: "+err.Error())
				break
			}
			if e.IsDir() {
				continue
			}
			load(filepath.Join(a.Path, e.Name()))
		}
	}
	return map[string]any{"loaded": loaded, "errors": failed}, nil
}

func (h *handler) listFormats(_ *http.Request, _ json.RawMessage) (any, error) {
	if h.rules == nil || h.rules.Registry() == nil {
		return nil, errors.New("parser registry is not available in this build")
	}
	var adapters []string
	for _, l := range h.rules.Registry().Loaders() {
		adapters = append(adapters, l.Name())
	}
	rules, err := h.rules.List()
	if err != nil {
		return nil, err
	}
	out := map[string]any{"adapters": adapters, "rules_dir": h.rules.Dir()}
	if rules == nil {
		out["rules"] = []any{}
	} else {
		out["rules"] = rules
	}
	return out, nil
}

func (h *handler) getFields(_ *http.Request, _ json.RawMessage) (any, error) {
	fields, err := h.eng.GetFields()
	if err != nil {
		return nil, err
	}
	return map[string]any{"fields": fields, "timestamp_field": h.eng.GetTimestampField()}, nil
}

func (h *handler) fieldSamples(_ *http.Request, args json.RawMessage) (any, error) {
	var a struct {
		Fields []string `json:"fields"`
		Cap    int      `json:"cap"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if len(a.Fields) == 0 {
		return nil, errors.New("fields is required")
	}
	if a.Cap <= 0 {
		a.Cap = defaultSampleCap
	}
	if a.Cap > maxSampleCap {
		a.Cap = maxSampleCap
	}
	samples, err := h.eng.FieldSamples(a.Fields, a.Cap)
	if err != nil {
		return nil, err
	}
	return map[string]any{"samples": samples, "cap": a.Cap}, nil
}

func (h *handler) query(_ *http.Request, args json.RawMessage) (any, error) {
	var req engine.QueryRequest
	if err := decode(args, &req); err != nil {
		return nil, err
	}
	if req.Limit <= 0 {
		req.Limit = defaultQueryRows
	}
	if req.Limit > h.opts.MaxRows {
		return nil, fmt.Errorf("limit %d exceeds max_rows %d; page with offset", req.Limit, h.opts.MaxRows)
	}
	if req.Offset < 0 {
		return nil, errors.New("offset must not be negative")
	}
	if req.SortOrder == "" {
		req.SortOrder = "desc"
	}
	return h.eng.Query(req)
}

func (h *handler) explainDetection(_ *http.Request, args json.RawMessage) (any, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		return nil, errors.New("path is required")
	}
	if h.rules == nil || h.rules.Registry() == nil {
		return nil, errors.New("parser registry is not available in this build")
	}
	if isZip(a.Path) {
		return nil, errZip
	}
	hint, err := ingest.HintForFile(a.Path)
	if err != nil {
		return nil, err
	}
	return h.rules.Registry().Explain(hint), nil
}

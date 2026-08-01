// Package ndjson is the Loader for newline-delimited JSON files.
//
// It is a "direct ingester": DuckDB's read_json_auto handles NDJSON
// natively, so we skip the Go-side streaming step and hand the original
// path straight to the engine. That keeps the fast path (2-4x quicker
// than parsing through Go) for the format most log shippers and export
// pipelines already emit.
package ndjson

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/labmk/obs-viewer/internal/ingest"
)

// Loader is the NDJSON adapter.
type Loader struct{}

// New returns a Loader ready to register.
func New() *Loader { return &Loader{} }

// Name reports the loader name.
func (Loader) Name() string { return "ndjson" }

// UseDirectPath is true: the engine should pass the original file path
// to DuckDB's read_json_auto. No streaming, no temp file.
func (Loader) UseDirectPath() bool { return true }

// Detect returns a confidence score for NDJSON.
//
// Strong match (90): extension is .ndjson, or the first non-whitespace
// byte is '{' AND the first line parses as a JSON object. NDJSON in
// the wild routinely lacks the .ndjson extension (export tools often
// write .json), so we don't require it.
//
// Weak match (50): extension is .json and the first byte is '{' — but
// we don't verify line-parse, since a top-level JSON array would also
// match and we don't want to claim those.
//
// No match (0): first byte is '[' (top-level JSON array), or no
// recognizable JSON start at all.
func (l Loader) Detect(h ingest.LoadHint) int {
	if h.Ext == ".ndjson" {
		return 90
	}
	trimmed := bytes.TrimLeft(h.Sniff, " \t\r\n")
	if len(trimmed) == 0 {
		return 0
	}
	// A leading '[' is a JSON array; not NDJSON.
	if trimmed[0] == '[' {
		return 0
	}
	if trimmed[0] != '{' {
		return 0
	}
	// First-line JSON parse check. We accept the score only if the very
	// first newline-terminated chunk is a complete JSON object — this
	// rules out arbitrary text that happens to start with '{' (e.g. a
	// log line beginning with a brace).
	if firstLineIsJSONObject(trimmed) {
		if h.Ext == ".json" {
			return 70
		}
		return 90
	}
	return 0
}

// firstLineIsJSONObject returns true when the first newline-delimited
// chunk of b unmarshals into a JSON object.
func firstLineIsJSONObject(b []byte) bool {
	nl := bytes.IndexByte(b, '\n')
	var line []byte
	if nl < 0 {
		line = b
	} else {
		line = b[:nl]
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return false
	}
	var v map[string]any
	dec := json.NewDecoder(strings.NewReader(string(line)))
	dec.UseNumber()
	return dec.Decode(&v) == nil
}

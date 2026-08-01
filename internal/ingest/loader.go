// Package ingest is the format-agnostic file-loader layer for obs_viewer.
//
// The engine knows nothing about NDJSON, XML, EVTX, or text logs anymore.
// It hands a file path to a Registry; the Registry sniffs the first ~512
// bytes, picks the best-matching Loader by confidence score, and either
//
//   - calls Stream() to emit a flat record stream that the engine writes
//     to a temp NDJSON file and then loads via DuckDB's read_json_auto, or
//   - asks the Loader to UseDirectPath() so the original file can be
//     handed to read_json_auto unchanged (the NDJSON fast path).
//
// Every Record carries a normalized "@timestamp" in ISO-8601 UTC so the
// engine's existing pickTimestampField/sort logic keeps working without
// per-format branching.
//
// Loaders live in subpackages (ndjson, block, line, xml, evtx) and are
// pure: no global state, no product-specific names in code. Per-format
// behavior is configured by YAML rule files merged from parsers.d/ at
// startup — see rules.go.
package ingest

import (
	"context"
	"time"
)

// Canonical field names every adapter must (or may) populate.
const (
	// FieldTimestamp is the normalized ISO-8601 UTC timestamp. Required.
	FieldTimestamp = "@timestamp"
	// FieldSource carries the loader name (e.g. "block", "line:iso-bracket").
	// Useful for filtering rows by which adapter produced them. Required.
	FieldSource = "_source_format"
	// FieldRaw, when set, carries the original record text (one line, one
	// XML element, one block, …). Optional — adapters set it when cheap.
	FieldRaw = "_raw"
)

// Record is one row emitted by an adapter. Keys become DuckDB columns.
// Values may be strings, numbers, bools, or nested map[string]any /
// []any structures (which DuckDB's JSON reader will keep as STRUCT/LIST
// types — the engine casts everything to VARCHAR for the UI anyway).
type Record map[string]any

// LoadHint is what a Loader sees when asked to handle a file.
type LoadHint struct {
	Path  string    // absolute path
	Ext   string    // lower-case extension including the dot, e.g. ".log"
	Sniff []byte    // up to 512 bytes from the start of the file
	MTime time.Time // file modification time — used by line loaders that
	// only carry a time-of-day to derive a date.
}

// Loader is the minimal interface every adapter implements.
//
// Detect returns a confidence score:
//
//	  0 — cannot handle this file
//	  1 — generic fallback (line-log default)
//	 50 — soft match (extension or weak content hint)
//	 80 — strong content match
//	100 — exact magic bytes (e.g. ElfFile for EVTX)
//
// The Registry picks the highest score. Ties are broken by alphabetic
// Name() order, which keeps the choice deterministic regardless of
// registration order.
type Loader interface {
	Name() string
	Detect(h LoadHint) int
}

// RecordStreamer is implemented by adapters that emit normalized records.
// Stream calls emit for each record; if emit returns a non-nil error,
// Stream must propagate it and stop. Stream returns nil on clean EOF.
//
// Adapters MUST set Record[FieldTimestamp] and Record[FieldSource]. Other
// fields are loader-specific. Multi-line / multi-event coalescing happens
// inside Stream so that one record = one logical row.
type RecordStreamer interface {
	Loader
	Stream(ctx context.Context, h LoadHint, emit func(Record) error) error
}

// DirectIngester is implemented by adapters whose input format is
// already directly readable by DuckDB's read_json_auto — i.e. NDJSON.
// The engine skips the streaming/temp-file detour and hands the original
// path to DuckDB. Any adapter that pre-converts to a temp file should
// implement RecordStreamer instead.
type DirectIngester interface {
	Loader
	UseDirectPath() bool
}

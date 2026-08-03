// Package parquet is the Loader for Apache Parquet files.
//
// Parquet is columnar: values are stored by column rather than by row,
// with the schema in a footer. That makes it the natural companion to
// this project's export — a filtered view written as Parquet keeps its
// types, and reading it back gets those types without re-inference.
//
// The round trip matters more than it sounds. NDJSON export loses type
// information the moment it is written: every value becomes text, and
// whoever reads it next re-guesses. Parquet does not, so exporting a
// result and loading it again is lossless.
//
// Like NDJSON this is a direct ingester — DuckDB reads the file itself,
// with no Go-side streaming and no temp file. Unlike NDJSON it needs a
// different read function, which is what DirectSQLIngester exists for.
// The parquet extension is statically linked into the binary, so this
// downloads nothing and works air-gapped.
package parquet

import (
	"bytes"

	"github.com/labmk/kopusha/internal/ingest"
)

// magic is the 4-byte marker at the start of every Parquet file. It also
// terminates the file, but the sniff buffer only covers the head.
//
// "PAR1" is the plain marker; "PARE" indicates an encrypted file, which
// DuckDB cannot read without a key, so it is deliberately not claimed.
const magic = "PAR1"

// Loader is the Parquet adapter.
type Loader struct{}

// New returns a Loader ready to register.
func New() *Loader { return &Loader{} }

// Name reports the loader name.
func (Loader) Name() string { return "parquet" }

// UseDirectPath is true: DuckDB reads the original file.
func (Loader) UseDirectPath() bool { return true }

// ReadExpr returns the DuckDB table expression for a Parquet file.
//
// union_by_name is not set here. It matters when reading several files
// as one relation, and this engine loads one file per table and unions
// them itself — schema reconciliation is the engine's job, not the
// reader's.
func (Loader) ReadExpr(escapedPath string) string {
	return "read_parquet('" + escapedPath + "')"
}

// Detect scores 100 on the magic bytes, which are unambiguous — no other
// format this project handles begins with "PAR1".
//
// The extension alone scores 90: a Parquet file too small to have been
// sniffed, or one behind a reader that returned nothing, is still worth
// claiming, and DuckDB will produce a clear error if it turns out not to
// be Parquet after all.
func (Loader) Detect(h ingest.LoadHint) int {
	if len(h.Sniff) >= len(magic) && bytes.Equal(h.Sniff[:len(magic)], []byte(magic)) {
		return 100
	}
	if h.Ext == ".parquet" || h.Ext == ".pq" {
		return 90
	}
	return 0
}

// Explain reports why Detect scored this file the way it did.
func (Loader) Explain(h ingest.LoadHint) string {
	if len(h.Sniff) >= len(magic) && bytes.Equal(h.Sniff[:len(magic)], []byte(magic)) {
		return "starts with the " + magic + " signature"
	}
	if h.Ext == ".parquet" || h.Ext == ".pq" {
		return "extension is " + h.Ext + ", though the " + magic + " signature is missing"
	}
	return "does not start with " + magic + ", and the extension is not .parquet"
}

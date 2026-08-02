// Package evtx is the Loader for Windows EVTX event-log files.
//
// EVTX is a binary format (magic bytes "ElfFile\0") emitted by Windows
// Vista and later. The parser is www.velocidex.com/golang/evtx
// (Apache-2.0).
//
// Each event is flattened into a single record keyed by dot-paths over
// the parsed structure. Standard fields land at predictable keys:
//
//	Event.System.EventID.Value
//	Event.System.Provider.Name        (XML attribute → flat key)
//	Event.System.TimeCreated.SystemTime
//	Event.System.Channel
//	Event.System.Computer
//	Event.EventData.<Name>            (or Event.UserData.<Group>.<Name>)
//
// @timestamp is derived from Event.System.TimeCreated.SystemTime, which
// the parser hands back as a float64 of Unix seconds, and normalized to
// RFC3339Nano UTC so the engine's existing sort logic works without
// special-casing EVTX.
//
// The adapter takes no rules — EVTX has a single well-defined shape.
package evtx

import (
	"context"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/Velocidex/ordereddict"
	"www.velocidex.com/golang/evtx"

	"github.com/labmk/obs-viewer/internal/ingest"
)

// evtxMagic is the 8-byte magic at the start of every EVTX file.
const evtxMagic = "ElfFile\x00"

// systemTimePath is where the canonical event timestamp lives once the
// record has been flattened.
const systemTimePath = "Event.System.TimeCreated.SystemTime"

// Loader is the EVTX adapter.
type Loader struct{}

// New returns a Loader ready to register.
func New() *Loader { return &Loader{} }

// Name reports the loader name.
func (Loader) Name() string { return "evtx" }

// Detect returns 100 when the file begins with the EVTX magic bytes.
// EVTX has no other recognizable form — there's no ambiguity to score.
func (Loader) Detect(h ingest.LoadHint) int {
	if len(h.Sniff) >= len(evtxMagic) && string(h.Sniff[:len(evtxMagic)]) == evtxMagic {
		return 100
	}
	return 0
}

// Explain reports why Detect scored this file the way it did.
func (Loader) Explain(h ingest.LoadHint) string {
	if len(h.Sniff) >= len(evtxMagic) && string(h.Sniff[:len(evtxMagic)]) == evtxMagic {
		return "starts with the ElfFile signature"
	}
	return "does not start with the ElfFile signature"
}

// Stream iterates every event in the file and emits one Record per
// event.
//
// The file is walked chunk by chunk. A chunk that fails to parse is
// skipped rather than aborting the file: event logs collected from a
// running system are routinely truncated mid-chunk, and the readable
// events before the damage are still worth having. If no chunk parses
// and at least one failed, the first failure is returned so a genuinely
// corrupt file doesn't look like an empty one.
//
// Everything happens on the caller's goroutine. The previous parser
// (0xrawsec/golang-evtx) fanned records out through a buffered channel
// filled by a producer goroutine that called panic() on malformed
// binary XML — unrecoverable from here, and fatal to the process. This
// parser returns errors instead, so a hostile file can no longer take
// the server down.
func (Loader) Stream(ctx context.Context, h ingest.LoadHint, emit func(ingest.Record) error) error {
	fd, err := os.Open(h.Path)
	if err != nil {
		return fmt.Errorf("evtx: open: %w", err)
	}
	defer fd.Close()

	chunks, err := evtx.GetChunks(fd)
	if err != nil {
		return fmt.Errorf("evtx: read chunks: %w", err)
	}

	var (
		emitted  int
		firstErr error
	)
	for _, chunk := range chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		records, err := chunk.Parse(0)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, r := range records {
			if err := ctx.Err(); err != nil {
				return err
			}
			dict, ok := r.Event.(*ordereddict.Dict)
			if !ok {
				continue
			}

			rec := ingest.Record{ingest.FieldSource: "evtx"}
			flattenDict(dict, "", rec)
			rec[ingest.FieldTimestamp] = eventTimestamp(rec, h.MTime)

			if err := emit(rec); err != nil {
				return err
			}
			emitted++
		}
	}

	if emitted == 0 && firstErr != nil {
		return fmt.Errorf("evtx: no readable events: %w", firstErr)
	}
	return nil
}

// eventTimestamp converts the parser's SystemTime into the ISO-8601 UTC
// string the ingest contract requires.
//
// The parser reports SystemTime as a float64 of Unix seconds with
// sub-second precision. Falling back to the file's mtime keeps the
// contract satisfied for a record whose header is damaged — a row with
// an approximate time still sorts and filters, where a row with no
// @timestamp at all would break the union query.
func eventTimestamp(rec ingest.Record, fallback time.Time) string {
	if raw, ok := rec[systemTimePath]; ok {
		if t, ok := unixFloatToTime(raw); ok {
			return t.UTC().Format(time.RFC3339Nano)
		}
	}
	return fallback.UTC().Format(time.RFC3339Nano)
}

// unixFloatToTime converts the numeric forms the parser may produce for
// a timestamp. Anything else reports false so the caller can fall back.
func unixFloatToTime(v any) (time.Time, bool) {
	var secs float64
	switch n := v.(type) {
	case float64:
		secs = n
	case float32:
		secs = float64(n)
	case int64:
		secs = float64(n)
	case int:
		secs = float64(n)
	case time.Time:
		return n, true
	default:
		return time.Time{}, false
	}
	if secs <= 0 || math.IsNaN(secs) || math.IsInf(secs, 0) {
		return time.Time{}, false
	}
	whole, frac := math.Modf(secs)
	return time.Unix(int64(whole), int64(frac*float64(time.Second))), true
}

// flattenDict walks the parsed event and writes every leaf value into
// out keyed by its dot-path. Mirrors the convention used by the XML
// adapter so users get a consistent column layout across formats.
//
// Value handling:
//   - nested *ordereddict.Dict → recurse
//   - []any                    → recurse with a .N index suffix per item
//   - scalar                   → store as-is (DuckDB infers the column type)
//
// An empty nested dict (EVTX emits `Correlation` and `Security` that way
// when unused) contributes no keys, which is what we want — an empty
// column per event would be noise in the field picker.
func flattenDict(d *ordereddict.Dict, prefix string, out ingest.Record) {
	for _, k := range d.Keys() {
		v, ok := d.Get(k)
		if !ok {
			continue
		}
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		flattenValue(v, key, out)
	}
}

// flattenValue dispatches a single value by shape. Split out from
// flattenDict so slice elements go through exactly the same logic as
// map values.
func flattenValue(v any, key string, out ingest.Record) {
	switch vv := v.(type) {
	case *ordereddict.Dict:
		flattenDict(vv, key, out)
	case map[string]any:
		// Defensive: not every value survives as a Dict.
		for k, item := range vv {
			flattenValue(item, key+"."+k, out)
		}
	case []any:
		for i, item := range vv {
			flattenValue(item, fmt.Sprintf("%s.%d", key, i), out)
		}
	default:
		out[key] = v
	}
}

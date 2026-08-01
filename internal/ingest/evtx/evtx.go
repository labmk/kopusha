// Package evtx is the Loader for Windows EVTX event-log files.
//
// EVTX is a binary format (magic bytes "ElfFile\0") emitted by Windows
// Vista and later. We use 0xrawsec/golang-evtx as the parser — it has
// a single dependency and is widely used in DFIR tooling.
//
// LICENSE WARNING: 0xrawsec/golang-evtx and its golang-utils dependency
// are GPL-3.0. Statically linking them makes a distributed binary a
// GPL-3.0 work, which obs-viewer's MIT license cannot carry. Resolving
// this is a prerequisite for publishing release binaries — see the
// migration TODO at the end of this file; Velocidex/evtx is Apache-2.0.
//
// Each event is flattened into a single record keyed by dot-paths over
// the original nested map. Standard fields land at predictable keys:
//
//	Event.System.EventID
//	Event.System.Provider.Name        (attribute → "Name", flat key)
//	Event.System.TimeCreated.SystemTime
//	Event.System.Channel
//	Event.System.Computer
//	Event.EventData.Data.<DataName>   (one entry per <Data Name="…">…)
//
// @timestamp is resolved from Event/System/TimeCreated/SystemTime and
// normalized to UTC RFC3339Nano so the engine's existing sort logic
// works without special-casing EVTX.
//
// The adapter takes no rules — EVTX has a single well-defined shape.
package evtx

import (
	"context"
	"fmt"
	"time"

	"github.com/0xrawsec/golang-evtx/evtx"

	"github.com/labmk/obs-viewer/internal/ingest"
)

// evtxMagic is the 8-byte magic at the start of every EVTX file.
const evtxMagic = "ElfFile\x00"

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

// Stream iterates every event in the file and emits one Record per
// event. Reads via OpenDirty so files that weren't cleanly closed
// (a common case for live-collected event logs) still parse instead
// of erroring out at the header check.
//
// Safety:
//   - defer ef.Close() so the file handle is released even on early
//     return (ctx cancel, emit error, panic).
//   - defer-drain the FastEvents channel. The library spawns a
//     producer goroutine that writes into a 42-deep buffered channel;
//     when our consumer loop exits early, the producer would block
//     forever on the next send (goroutine leak). Reading the channel
//     to close unblocks it.
//   - Panic recovery: 0xrawsec/golang-evtx panics on malformed binary
//     XML chunks (`panic(err)` inside FastEvents' inner goroutine).
//     Those panics happen in the library's own goroutine and CANNOT
//     be caught here — they crash the whole program. The real fix is
//     to migrate to Velocidex/evtx, which doesn't use this panic
//     pattern; see TODO at the end of this file.
func (Loader) Stream(ctx context.Context, h ingest.LoadHint, emit func(ingest.Record) error) error {
	ef, err := evtx.OpenDirty(h.Path)
	if err != nil {
		return fmt.Errorf("evtx: open: %w", err)
	}
	defer ef.Close()

	events := ef.FastEvents()
	defer func() {
		// Drain any events still in flight so the producer goroutine
		// can exit cleanly. No-op when we ran to completion (channel
		// already closed); the cost on early return is bounded by the
		// library's 42-element buffer plus its internal chunk queue.
		for range events {
		}
	}()

	for e := range events {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e == nil {
			continue
		}
		rec := ingest.Record{ingest.FieldSource: "evtx"}
		flattenEvtx(*e, "", rec)

		// Override @timestamp using the canonical SystemTime path so
		// downstream sort/range works regardless of how the raw map
		// represented the time. UTC + RFC3339Nano.
		if t, err := e.GetTime(&evtx.SystemTimePath); err == nil && !t.IsZero() {
			rec[ingest.FieldTimestamp] = t.UTC().Format(time.RFC3339Nano)
		}

		if err := emit(rec); err != nil {
			return err
		}
	}
	return nil
}

// TODO(security): migrate to `www.velocidex.com/golang/evtx`.
// 0xrawsec/golang-evtx has not seen a release since 2020 and panics on
// malformed binary input from inside its producer goroutine — that
// crashes the whole process and cannot be recovered from here.
// Velocidex's fork is actively maintained, used in Velociraptor DFIR,
// and uses an error-returning chunk iterator instead of panic. The
// migration is blocked in this build environment by lack of access to
// proxy.golang.org; perform it from a connected workstation:
//
//   go get www.velocidex.com/golang/evtx@latest
//   # then swap the import + adapt event iteration; the GoEvtxMap
//   # shape is similar enough that flattenEvtx needs minimal changes.

// flattenEvtx walks the nested GoEvtxMap and writes every leaf value
// into out keyed by its dot-path. Mirrors the convention used by the
// XML adapter so users get a consistent column layout across formats.
//
// Value handling:
//   - nested GoEvtxMap  → recurse
//   - []GoEvtxElement   → recurse with .N index suffix per item
//   - scalar            → store as-is (DuckDB will infer the column type)
func flattenEvtx(m evtx.GoEvtxMap, prefix string, out ingest.Record) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch vv := v.(type) {
		case evtx.GoEvtxMap:
			flattenEvtx(vv, key, out)
		case map[string]interface{}:
			// Defensive: some chunks deserialize into bare maps.
			flattenEvtx(evtx.GoEvtxMap(vv), key, out)
		case []evtx.GoEvtxElement:
			for i, item := range vv {
				ikey := fmt.Sprintf("%s.%d", key, i)
				switch iv := item.(type) {
				case evtx.GoEvtxMap:
					flattenEvtx(iv, ikey, out)
				default:
					out[ikey] = iv
				}
			}
		case []interface{}:
			for i, item := range vv {
				ikey := fmt.Sprintf("%s.%d", key, i)
				if sub, ok := item.(evtx.GoEvtxMap); ok {
					flattenEvtx(sub, ikey, out)
				} else {
					out[ikey] = item
				}
			}
		default:
			out[key] = v
		}
	}
}

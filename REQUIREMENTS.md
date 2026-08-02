# obs-viewer — Data-type ingestion requirements

This document is the canonical list of file formats obs-viewer accepts,
the contract each one must satisfy after ingest, and the test cases
that prove it. Update this file together with the test matrix below
whenever a new adapter or shipped `parsers.d/` rule is added.

## Fixtures

Every fixture under `test-fixtures/` is **synthetic** and is produced by
`test-fixtures/generate.py`, which is seeded so regeneration is
byte-reproducible:

```bash
python3 test-fixtures/generate.py
```

Regenerate after changing any `parsers.d/` rule, then run
`go test ./internal/ingest/` — `fixtures_test.go` wires up the same
registry `main.go` builds and asserts every fixture still routes to its
intended adapter and emits the documented `_source_format`. That test is
what catches the quiet failure where an edited regex stops matching, a
lower-priority rule picks the file up instead, rows still appear, and
nothing looks wrong until you check the marker.

Fixtures exercise format grammar only. See the "Test data policy"
section in CONTRIBUTING.md for what they must never contain.

**EVTX is the one vendored fixture.** It is a binary format with
per-chunk CRC32 checksums and BinXML template tables, which
`generate.py` cannot synthesize. Shipping a capture of our own is out
of the question — an exported Windows event log carries the machine
name, user SIDs, and installed-software traces of whatever host
produced it. But shipping *nothing* left REQ-DT-02 skipped and every
EVTX code path unproven in CI, which is worse.

So `test-fixtures/formats/sample.evtx` is vendored from the
[Velocidex/evtx](https://github.com/Velocidex/evtx) project
(`testdata/Security_1_record.evtx`, Apache-2.0, 68 KB). It is an
already-published sample from a third party, so it discloses nothing
about any of our environments — the policy above is satisfied at its
intent, not merely its letter. It is a single-record Security log whose
header is marked **dirty**, which usefully exercises the `OpenDirty`
path that a cleanly-closed export would not reach. Attribution lives in
`THIRD-PARTY-NOTICES.md`.

This is the only fixture permitted to come from outside; everything
else is generated. The skip-if-missing guards stay in place so the
suite still degrades gracefully if someone deletes the file:

- `internal/ingest/evtx/evtx_test.go` — magic-byte detection tests run
  unconditionally (they construct a `LoadHint` in memory); the
  streaming test skips when the file is absent.
- `internal/ingest/fixtures_test.go` — REQ-DT-02 is marked optional.
- `frontend/e2e/formats.spec.js` — REQ-DT-02 skips.

To test against a different capture, drop any `.evtx` file in its
place; on Windows, `wevtutil epl Application sample.evtx` produces one.
Do not commit a replacement sourced from a real machine.

## Ingestion contract

**For every data type:**

1. Be queryable through `/api/query` with the file enabled — i.e. the
   row count from `/api/files` matches the `total_count` returned by
   an unfiltered `/api/query` for that file alone.
2. Reach the engine via the dispatcher in `internal/ingest/dispatch.go`
   (sniff + extension routing). Detection scores >= 70 commit the file
   to that adapter.

**For every data type EXCEPT NDJSON:**

3. Carry a top-level **`@timestamp`** field as an ISO-8601 UTC string.
4. Carry a top-level **`_source_format`** field whose value matches
   the canonical name listed in the table below. This field is injected
   by the per-adapter record-streaming layer.

**NDJSON and Parquet are the documented exceptions:** both go through
`DirectIngester`
(DuckDB's `read_json_auto` reads the original file directly), so the
records carry only their original fields — no `_source_format`
injection. Parquet is the same case for the same reason, and more
strongly: a Parquet file is *already* a typed table, so re-deriving a
schema or injecting marker columns would discard what the format exists
to preserve. Round-tripping an export back in must return what went
out. The original NDJSON is already structured; preserving the
schema as-written is the whole point of the direct path (2-4× faster
than parsing through Go). NDJSON detection is verified via the
`/api/files` `format` field instead.

The dispatcher (`internal/ingest/dispatch.go`) routes by sniff +
extension. Detection scores >= 70 commit the file to that adapter;
below that the adapter is treated as fallback. Each requirement
below states the expected detection signal so a regression in
sniffing is easy to spot.

## Data type matrix

| ID | Format family | Rule / variant | `_source_format` | Detection signal | Sample fixture | Go unit test | E2e test |
|----|---------------|----------------|------------------|------------------|----------------|--------------|----------|
| REQ-DT-01 | NDJSON | n/a (direct) | n/a (no `_source_format` — direct path) | `.ndjson` extension OR first non-blank line parses as a JSON object | `test-fixtures/formats/sample.ndjson` | `internal/ingest/ndjson/ndjson_test.go::TestDirectIngester` | `frontend/e2e/formats.spec.js::REQ-DT-01 ndjson` |
| REQ-DT-10 | Parquet | n/a (columnar binary) | n/a (no `_source_format` — direct path) | Magic bytes `PAR1` at offset 0, or `.parquet`/`.pq` extension | `test-fixtures/formats/sample.parquet` | `internal/ingest/parquet/parquet_test.go` | `frontend/e2e/formats.spec.js::parquet` |
| REQ-DT-02 | EVTX | n/a (binary) | `evtx` | Magic bytes `ElfFile\0` at offset 0 | `test-fixtures/formats/sample.evtx` | `internal/ingest/evtx/evtx_test.go::TestStreamRealSample` (skip-if-missing) | `frontend/e2e/formats.spec.js::evtx` |
| REQ-DT-03 | XML | `repeating-row-element` | `xml:<rowName>` | First non-whitespace char is `<` | `test-fixtures/formats/xml-row-element.txt` | `internal/ingest/xml/xml_test.go::TestMultiRootStreamingWithoutXmlDeclaration` (et al.) | `frontend/e2e/formats.spec.js::xml` |
| REQ-DT-04 | block | `keyvalue-dash-separated` | `block:keyvalue-dash-separated` | A line of 20+ dashes appears in the first ~512 bytes | `test-fixtures/formats/block-keyvalue-dash.txt` | `internal/ingest/block/block_test.go::TestDetectMatchesDashSeparator` (et al.) | `frontend/e2e/formats.spec.js::block-keyvalue-dash` |
| REQ-DT-05 | line | `iso-bracket` | `line:iso-bracket` | Parse regex matches first non-blank line: `YYYY-MM-DD HH:MM:SS.mmm [LVL] [COMP] [PID:N] msg` | `test-fixtures/formats/line-iso-bracket.log` | `internal/ingest/line/line_test.go::TestIsoBracketBasic` / `TestIsoBracketContinuation` | `frontend/e2e/formats.spec.js::line-iso-bracket` |
| REQ-DT-06 | line | `dashdate-level` | `line:dashdate-level` | Parse regex matches: `DD-MM-YYYY HH:MM:SS.mmm Level msg` | `test-fixtures/formats/line-dashdate-level.log` | `internal/ingest/line/line_test.go::TestDashdateLevel` | `frontend/e2e/formats.spec.js::line-dashdate-level` |
| REQ-DT-07 | line | `dotdate-pidtid` | `line:dotdate-pidtid` | Parse regex matches: `prefix: DD.MM.YY HH:MM:SS:msec PID/TID msg` (note colon-msec — handled by `ts_regex_subs`) | `test-fixtures/formats/line-dotdate-pidtid.log` | `internal/ingest/line/line_test.go::TestDotdatePIDTID` | `frontend/e2e/formats.spec.js::line-dotdate-pidtid` |
| REQ-DT-08 | line | `time-pidtid` | `line:time-pidtid` | Parse regex matches: `HH:MM:SS.uuuuuu PID/TID Level msg` (date sourced from file mtime, rollover-aware) | `test-fixtures/formats/line-time-pidtid.log` | `internal/ingest/line/line_test.go::TestTimeOnlyUsesMtimeDate` + `TestTimeOnlyDayRollover` | `frontend/e2e/formats.spec.js::line-time-pidtid` |
| REQ-DT-09 | line | `time-dotdate` | `line:time-dotdate` | Parse regex matches: `HH:MM:SS.mmm DD.MM.YYYY: msg` (time before date) | `test-fixtures/formats/line-time-dotdate.log` | `internal/ingest/line/line_test.go::TestTimeDotdate` | `frontend/e2e/formats.spec.js::line-time-dotdate` |

## Adding a new data type

1. **New format family** (e.g. CSV, syslog): create `internal/ingest/<name>/`, implement the `Loader` interface (plus `RecordStreamer` or `DirectIngester`), register from `main.go`. Add Go unit tests under `internal/ingest/<name>/`. Add a new REQ-DT-NN row to the matrix above and a fixture under `test-fixtures/formats/`.

2. **Same family, new rule variant** (another line/block/xml shape): drop a YAML file under `parsers.d/`, add a writer to `test-fixtures/generate.py` and regenerate, add a Go unit test (the existing per-family `_test.go` files already cover the test harness — just add a new `TestX` function). Add a REQ-DT-NN row to the matrix.

3. Add a row to the table in `internal/ingest/fixtures_test.go` so the new fixture's routing is asserted.

4. Add an e2e test in `frontend/e2e/formats.spec.js` mirroring the existing per-data-type structure (POST `/api/files/load`, GET `/api/files` for the row count, POST `/api/query` and assert `_source_format`).

## Running the test matrix

```bash
# Go unit tests across every adapter:
cd obs-viewer && go test ./internal/ingest/...

# E2e suite (requires dist/obs_viewer.exe built first):
cd obs-viewer/frontend && npm run test:e2e -- formats.spec.js
```

CI should fail if any new data type lands without a row in the matrix
above and a fixture + Go unit + e2e covering it.

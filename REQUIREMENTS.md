# Format requirements

This is the list of file formats kopusha supports, what each one must
produce after loading, and the tests that check it. A format without a
row in the [matrix](#format-matrix) is not considered supported. Update
this file together with the matrix whenever an adapter or a shipped
`parsers.d/` rule is added.

## Ingest contract

For every format:

1. The file is loaded through the dispatcher in
   `internal/ingest/dispatch.go`, which picks the adapter with the
   highest detection score (see
   [ARCHITECTURE.md](./ARCHITECTURE.md#detection)).
2. The file can be queried: its record count in `/api/files` equals
   `total_count` from an unfiltered `/api/query` with only that file
   enabled.

For every format except NDJSON and Parquet:

3. Each record has a top-level `@timestamp` field, an ISO-8601 UTC
   string.
4. Each record has a top-level `_source_format` field with the value
   listed in the matrix. The adapter's record stream adds it.

NDJSON and Parquet are read by DuckDB directly from the original file
(`read_json_auto` and `read_parquet`), so their records keep exactly
their original fields and get no `_source_format`. For NDJSON this
keeps the schema as written and is 2 to 4 times faster than parsing in
Go. A Parquet file is already a typed table; adding columns would
change it, and an exported file loaded back must return what was
exported. These two formats are checked through the `format` field of
`/api/files` instead.

## Format matrix

| ID | Family | Rule or variant | `_source_format` | Detection | Fixture | Go test | E2e test |
|----|--------|-----------------|------------------|-----------|---------|---------|----------|
| REQ-DT-01 | NDJSON | direct | none | `.ndjson` extension, or the first line is a JSON object | `test-fixtures/formats/sample.ndjson` | `internal/ingest/ndjson/ndjson_test.go::TestDirectIngester` | `frontend/e2e/formats.spec.js::REQ-DT-01 ndjson` |
| REQ-DT-10 | Parquet | direct | none | `PAR1` magic bytes, or `.parquet` / `.pq` extension | `test-fixtures/formats/sample.parquet` | `internal/ingest/parquet/parquet_test.go` | none yet; routing is covered by `internal/ingest/fixtures_test.go` |
| REQ-DT-02 | EVTX | binary | `evtx` | `ElfFile\0` magic bytes | `test-fixtures/formats/sample.evtx` | `internal/ingest/evtx/evtx_test.go::TestStreamRealSample` (skipped if the file is missing) | `frontend/e2e/formats.spec.js::REQ-DT-02 evtx` |
| REQ-DT-03 | XML | `repeating-row-element` | `xml:<row name>` | First non-whitespace character is `<` | `test-fixtures/formats/xml-row-element.txt` | `internal/ingest/xml/xml_test.go::TestMultiRootStreamingWithoutXmlDeclaration` and others | `frontend/e2e/formats.spec.js::REQ-DT-03 xml-row-element` |
| REQ-DT-04 | block | `keyvalue-dash-separated` | `block:keyvalue-dash-separated` | A line of 20 or more dashes in the first 512 bytes | `test-fixtures/formats/block-keyvalue-dash.txt` | `internal/ingest/block/block_test.go::TestDetectMatchesDashSeparator` and others | `frontend/e2e/formats.spec.js::REQ-DT-04 block-keyvalue-dash` |
| REQ-DT-05 | line | `iso-bracket` | `line:iso-bracket` | `YYYY-MM-DD HH:MM:SS.mmm [LVL] [COMP] [PID:N] msg` | `test-fixtures/formats/line-iso-bracket.log` | `internal/ingest/line/line_test.go::TestIsoBracketBasic`, `TestIsoBracketContinuation` | `frontend/e2e/formats.spec.js::REQ-DT-05 line-iso-bracket` |
| REQ-DT-06 | line | `dashdate-level` | `line:dashdate-level` | `DD-MM-YYYY HH:MM:SS.mmm Level msg` | `test-fixtures/formats/line-dashdate-level.log` | `internal/ingest/line/line_test.go::TestDashdateLevel` | `frontend/e2e/formats.spec.js::REQ-DT-06 line-dashdate-level` |
| REQ-DT-07 | line | `dotdate-pidtid` | `line:dotdate-pidtid` | `prefix: DD.MM.YY HH:MM:SS:msec PID/TID msg` (the colon before milliseconds is fixed by `ts_regex_subs`) | `test-fixtures/formats/line-dotdate-pidtid.log` | `internal/ingest/line/line_test.go::TestDotdatePIDTID` | `frontend/e2e/formats.spec.js::REQ-DT-07 line-dotdate-pidtid` |
| REQ-DT-08 | line | `time-pidtid` | `line:time-pidtid` | `HH:MM:SS.uuuuuu PID/TID Level msg` (date from file modification time, handles midnight rollover) | `test-fixtures/formats/line-time-pidtid.log` | `internal/ingest/line/line_test.go::TestTimeOnlyUsesMtimeDate`, `TestTimeOnlyDayRollover` | `frontend/e2e/formats.spec.js::REQ-DT-08 line-time-pidtid` |
| REQ-DT-09 | line | `time-dotdate` | `line:time-dotdate` | `HH:MM:SS.mmm DD.MM.YYYY: msg` (time before date) | `test-fixtures/formats/line-time-dotdate.log` | `internal/ingest/line/line_test.go::TestTimeDotdate` | `frontend/e2e/formats.spec.js::REQ-DT-09 line-time-dotdate` |

### Unrecognised format

| ID | Requirement | Fixture | Go test | E2e test |
|----|-------------|---------|---------|----------|
| REQ-DT-00 | No adapter accepts the file, and every adapter reports a reason, the first non-blank line, and any encoding problem (byte-order mark, CRLF, NUL bytes, invalid UTF-8) | `test-fixtures/formats/unmatched.log` | `internal/server/rules_test.go::TestExplainReportsEveryAdapter` | `frontend/e2e/rules.spec.js` |

This fixture must keep matching nothing. If a new shipped rule starts
matching it, change the fixture, not the test, and keep it a plausible
real-world format.

## Fixtures

All fixtures in `test-fixtures/` are synthetic. The NDJSON, XML,
block and line fixtures are produced by `test-fixtures/generate.py`,
which uses a fixed seed, so running it again gives identical bytes.
`sample.parquet` and `unmatched.log` are committed files that the
generator does not write, and `sample.evtx` is described below.

```bash
python3 test-fixtures/generate.py
```

Regenerate after changing a `parsers.d/` rule, then run
`go test ./internal/ingest/`. `internal/ingest/fixtures_test.go` builds
the same adapter registry as `main.go` and checks that each fixture is
picked by the intended adapter and has the expected `_source_format`.
This catches the case where an edited regex stops matching, a
lower-priority rule takes the file instead, and rows still appear.

Fixtures describe format structure only. The rules for their content
are in [CONTRIBUTING.md](./CONTRIBUTING.md#test-data-policy).

### Fixtures in release archives

`build.sh` copies a fixed subset into `dist/samples/`, which every
release archive includes: `sample.ndjson`, `sample.parquet`,
`xml-row-element.txt` and `unmatched.log`, plus
`test-fixtures/SAMPLES.md` as `README.md`. These are therefore
published to anyone who downloads kopusha.

### The EVTX fixture

EVTX is a binary format with per-chunk CRC32 checksums and BinXML
templates that `generate.py` cannot produce. An export from one of our
own machines would contain its host name, user SIDs and installed
software. So `test-fixtures/formats/sample.evtx` is taken from the
[Velocidex/evtx](https://github.com/Velocidex/evtx) project
(`testdata/Security_1_record.evtx`, Apache-2.0, 68 KB). It is a public
third-party sample and reveals nothing about our environments. It holds
one Security record and its header is marked dirty, which tests the
`OpenDirty` code path.

It is the only fixture from outside the project. It is not copied into
release archives: `THIRD-PARTY-NOTICES.md` is generated from the Go
dependency graph and does not cover data files, so shipping it would
add an attribution duty that the archive does not meet.

If the file is missing, the tests degrade instead of failing:

- `internal/ingest/evtx/evtx_test.go`: magic-byte tests still run; the
  streaming test is skipped.
- `internal/ingest/fixtures_test.go`: REQ-DT-02 is optional.
- `frontend/e2e/formats.spec.js`: REQ-DT-02 is skipped.

To test with another file, put any `.evtx` in its place (on Windows,
`wevtutil epl Application sample.evtx` creates one). Do not commit a
file taken from a real machine.

## Adding a format

1. **New variant of an existing family** (another line, block or XML
   shape): build the rule in the UI (**Parser rules**) or add a YAML
   file to `parsers.d/`. Add a writer to `test-fixtures/generate.py`
   and regenerate. Add a test function to the family's existing
   `_test.go` file.
2. **New family** (for example CSV): create `internal/ingest/<name>/`,
   implement `Loader` plus `RecordStreamer` or `DirectIngester`, and
   `Explainer` so the adapter can say why it declined. Register it in
   `parsers.Manager.Reload`. If the format has its own extension, add it
   to `ingestableExts` in `internal/server/server.go`, or the file
   browser hides it. Add unit tests in `internal/ingest/<name>/` and a
   fixture in `test-fixtures/formats/`.
3. Either way: add a REQ-DT row to the matrix above, a row to the table
   in `internal/ingest/fixtures_test.go`, and an e2e test in
   `frontend/e2e/formats.spec.js` that follows the existing ones (POST
   `/api/files/load`, GET `/api/files` for the record count, POST
   `/api/query` and check `_source_format`).
4. A shipped rule change also changes `parsers.d.sha256`; see
   [CONTRIBUTING.md](./CONTRIBUTING.md#adding-a-log-format).

## Running the tests

From the repository root:

```bash
go test ./internal/ingest/...                          # adapter unit tests

./build.sh                                             # e2e needs dist/kopusha (dist/kopusha.exe on Windows)
cd frontend && npm run test:e2e -- formats.spec.js     # format e2e tests
```

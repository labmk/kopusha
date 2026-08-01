# Architecture

How obs-viewer is put together: the layers, the contracts between
them, and the decisions that are load-bearing. Working rules for
contributors live in [CONTRIBUTING.md](./CONTRIBUTING.md); the ingest
contract and its test matrix live in [REQUIREMENTS.md](./REQUIREMENTS.md).

## What This Is

obs-viewer is a standalone log/metrics/traces viewer distributed as a
single static binary: Go backend + embedded DuckDB query engine +
React/Vite frontend bundled via `go:embed`. It reads files from the
local filesystem — NDJSON, Windows EVTX, XML, and rule-driven text logs
— and exposes a query UI on localhost. No server, no index, no runtime
dependencies.

Primary target is Windows amd64; Linux amd64 and macOS arm64 build from
the same source with no code changes.

## Repository layout

```
obs_viewer[.exe]                     (~85 MB single binary)
  main.go                            CLI entry: flags, conf load, registry boot, TLS
  console_windows.go                 AttachConsole shim (build-tagged); console_other.go is a no-op
  internal/
    config/config.go                 obs_viewer*.conf parser (INI sections) + LoadAll merge
    engine/engine.go                 DuckDB wrapper: dispatch via ingest, query, export
    ingest/                          Format dispatch + adapters
      loader.go                      Loader, RecordStreamer, DirectIngester ifaces
      dispatch.go                    Registry: sniff → pick by confidence score
      rules.go                       parsers.d/ YAML loader; family-routed
      ndjson/                        DirectIngester (DuckDB read_json_auto fast path)
      block/                         ---- separated Key: Value records
      line/                          regex-driven line autodetect
      xml/                           autodetected row element + dot-path flatten
      evtx/                          Windows EVTX via Velocidex/evtx
    logx/                            Structured file logger next to the binary
    manifest/                        SHA-256 record of the shipped parsers.d/ rules
    update/                          Release-notification check (reports only)
    module/                          Module interface + Registry + /api/modules
    server/server.go                 Core HTTP server + embedded SPA
    server/docs/                     swag-generated OpenAPI spec (regen via `swag init`)
    settings/settings.go             Persistent settings (JSON file next to binary)
  frontend/                          React SPA (Vite build)
    openapi-ts.config.js             reads internal/server/docs/swagger.json
    playwright.config.js             spawns dist/obs_viewer.exe on :9201 with --dir test-fixtures/ndjson
    src/
      App.jsx                        Layout, /api/modules consumer
      main.jsx                       Wraps App in QueryClientProvider
      queryClient.js                 TanStack Query client + defaults
      moduleRegistry.js              Maps module id → React component (empty by default)
      api/client.js                  Hand-written HTTP client (coexists with generated)
      api/generated/                 @hey-api/openapi-ts output — types + SDK
      hooks/useLocalStorage.js       Lazy-init + auto-persist hook
      hooks/useApiQueries.js         useVersion/useFiles/useFields/useLogQuery/…
      utils/datetime.js              Shared date parse/format/validate
      utils/queryDsl.js              LogQL-shaped serialize/parse
      components/                    FilePanel, FileBrowser, TimeFilter, QueryBuilder,
                                     QueryTextDialog, MultiValueInput, LogTable,
                                     FieldPicker, ExportDialog
      styles/app.css                 Neutral slate theme + .rdx-* overlay hooks
    e2e/                             Playwright smoke + per-format specs
  test-fixtures/                     Synthetic + one vendored EVTX sample; see REQUIREMENTS.md
    ndjson/                          pre-loaded by webServer for smoke.spec.js
    formats/                         one fixture per REQ-DT entry
  cmd/genmanifest/                   Writes parsers.d.sha256; run from build.sh
  parsers.d/                         YAML rule files for block/line/xml autodetect
  parsers.d.sha256                   Generated; go:embed'd so the binary knows
                                     which rules it shipped with
  modules/                           Optional sub-features (none ship by default)
  static/                            Vite build output (generated, git-ignored)
  ARCHITECTURE.md                    This document
  REQUIREMENTS.md                    Canonical accepted data types + ingest contract
  docs/MODULES.md                    Module authoring contract
  docs/BUILD.md                      Per-platform build + cross-compile notes
```

## Tech Stack

- **Backend**: Go 1.25.6+ (toolchain pinned to 1.26.5), `net/http`,
  `embed`, `crypto/tls` — all stdlib. `github.com/swaggo/swag` for
  OpenAPI annotations.
- **Query engine**: DuckDB via `github.com/duckdb/duckdb-go/v2` (CGO
  required; statically-linked `libduckdb_static.a` from
  `duckdb-go-bindings`).
- **Frontend**: React 18, Vite 6, react-window (virtual scrolling),
  TanStack Query 5 (server state + caching), Radix UI primitives
  (Dialog + Popover).
- **Codegen**: `@hey-api/openapi-ts` generates the TS client from
  `swag init`'s `swagger.json` into `frontend/src/api/generated/` on
  every `npm run build` (via the `prebuild` script).
- **e2e**: Playwright against the real Go binary with pre-loaded
  fixtures.
- **Build**: `build.sh` runs `swag init` (if available) → `npm ci &&
  npm run build` → `go build`.

### Toolchain setup

- **Go**: 1.25.6+ with CGO enabled.
- **Node**: 18+ with npm.
- **C compiler (Windows)**: a MinGW-w64 GCC whose C++ ABI matches the
  prebuilt `libduckdb_static.a`. `build.sh` probes for one and reports
  its choice. The working set moves with `duckdb-go-bindings` releases —
  docs/BUILD.md records what was last verified.
- **DuckDB runtime**: no external download. Extensions (JSON, ICU,
  parquet, …) are statically linked; never call `INSTALL json` — it
  would try to fetch from `extensions.duckdb.org` and hang ~4 minutes
  on air-gapped hosts.

## Key Design Decisions

- **Single binary**: frontend compiled in via `go:embed static/*`. This
  is what enables the self-copy-with-export feature, where the binary
  is copied alongside exported data so the recipient needs no setup.
- **Backend file browser**: browsers cannot read the local filesystem,
  so the Go backend exposes `/api/browse`. `--files`/`--dir` cover the
  scripted path.
- **DuckDB for NDJSON**: native `read_json_auto()` with
  `format='newline_delimited'`. Columnar in-memory representation is
  2–4× more memory-efficient than parsed JSON dicts. All filtering maps
  to SQL (`ILIKE` for case-insensitive; `*` is translated to `%`).
- **Heterogeneous schemas**: the column set is the union of all enabled
  tables; `UNION ALL` queries name columns explicitly and fill `NULL`
  where a file lacks one.
- **Timestamp handling**: auto-detected by column name (priority
  `@timestamp` → `timestamp` → `time` → …). Stored UTC; the frontend
  applies presentation timezone.
- **Loopback by default**: a non-loopback `listen` is honoured only
  when a TLS cert is supplied; otherwise the server warns and falls
  back to `127.0.0.1`. There is no authentication layer.

## Ingest layer

`engine.LoadFile` builds a `LoadHint` (path, extension, first 512
bytes, mtime), asks `ingest.Registry` to pick the best Loader by
confidence score, and then either:

- calls `UseDirectPath()` and hands the original path to DuckDB
  unchanged (NDJSON), or
- calls `Stream(ctx, hint, emit)`, which iterates the file and writes
  one normalized `ingest.Record` per logical row into a temp NDJSON
  file that DuckDB then loads via the same `read_json_auto`.

The temp file is deleted after the table is created. `FileInfo.Path`
reported to the UI is always the original input path, never the temp.

### Loader contract

```go
type Loader interface {
    Name() string
    Detect(h LoadHint) int  // 0=no, 100=exact magic, 1=fallback
}

type RecordStreamer interface {
    Loader
    Stream(ctx context.Context, h LoadHint, emit func(Record) error) error
}

type DirectIngester interface {
    Loader
    UseDirectPath() bool
}
```

Every `Record` must include `@timestamp` (ISO-8601 UTC) and
`_source_format` (e.g. `line:iso-bracket`, `xml:Event`,
`block:keyvalue-dash-separated`, `evtx`). NDJSON is the documented
exception — see REQUIREMENTS.md.

### Dispatch

`Registry.Pick` returns the highest-scoring loader; ties break
alphabetically by `Loader.Name()` for determinism.

| Score | Meaning |
|-------|---------|
| 100   | Exact magic bytes (EVTX `ElfFile\0`) |
| 90    | Strong extension match (`.ndjson`) or validated content (first-line JSON object) |
| 70–85 | Content sniff matched (block separator, line regex, xml `<`) |
| 1–50  | Fallback / weak match |
| 0     | Cannot handle |

### Rule files (`parsers.d/`)

Each YAML file is one rule. The top-level `family:` key routes it to
`block`, `line`, or `xml`; unknown families log a warning and are
ignored. Files load in lexicographic order, so a numeric prefix gives
precedence control.

| File | Family | Matches |
|------|--------|---------|
| `00-block-keyvalue-dash.yaml` | block | 20+-dash separator, `Key: Value` fields |
| `10-xml-row-element.yaml` | xml | any XML; autodetects row element |
| `20-line-iso-bracket.yaml` | line | `YYYY-MM-DD HH:MM:SS.mmm [LVL] [COMP] [PID:N] msg` |
| `21-line-dashdate-level.yaml` | line | `DD-MM-YYYY HH:MM:SS.mmm Level msg` |
| `22-line-dotdate-pidtid.yaml` | line | `prefix: DD.MM.YY HH:MM:SS:mmm PID/TID msg` |
| `23-line-time-pidtid.yaml` | line | `HH:MM:SS.uuuuuu PID/TID Level msg` (date from mtime, rollover-aware) |
| `24-line-time-dotdate.yaml` | line | `HH:MM:SS.mmm DD.MM.YYYY: msg` |

The `line` family supports `ts_regex_subs` (preprocess the captured ts
before `time.Parse`) and `ts_use_mtime_date` (combine time-of-day with
file mtime; a backwards jump > 12h advances the working date by one day
for cross-midnight rollover). The `xml` family always dot-path flattens
with `@`-prefixed attributes. The `block` family handles UTF-8 BOM and
CRLF transparently.

**Rules must describe structure only** — regexes, dot-paths, field
names. No product names, hostnames, or deployment specifics.

### Adding a new format

1. **Same family as an existing rule**: drop a YAML file in
   `parsers.d/`, restart.
2. **Brand-new family** (CSV, syslog, …): create
   `internal/ingest/<name>/`, implement `Loader` + `RecordStreamer` (or
   `DirectIngester`), register in `main.go`. No engine changes needed.

Either way, add a REQ-DT row + fixture + Go test + e2e test — see the
CI gate in REQUIREMENTS.md.

## Engine internals

`engine.Engine` caches per-table metadata to keep `information_schema`
off the hot path:

| Field | Type | Populated | Cleared | Purpose |
|-------|------|-----------|---------|---------|
| `tableCols` | `map[string][]string` | `LoadFile` | `UnloadFile` | Ordered column list per DuckDB table |
| `tableStructPaths` | `map[string][]string` | `LoadFile` | `UnloadFile` | Dotted STRUCT sub-paths discovered by probing |
| `pathIndex` | `map[string]string` | `LoadFile` | `UnloadFile` | Absolute path → file ID, O(1) duplicate detection |

Key helpers:

- `fetchTableColumns(tableName)` — hits `information_schema`. **Only
  called from `LoadFile`**; runtime callers read `e.tableCols`.
- `allColumns(tables)` — union across tables, first-appearance order.
- `buildUnionQuery(tables, addSourceTable)` — `UNION ALL` from cache.
- `pickTimestampField(columns)` / `isTimestampLike(col)` — pure helpers.

### STRUCT sub-path filtering

DuckDB's `read_json_auto` materialises nested JSON as `STRUCT` columns:
`{"nodeinfo": {"type": "worker"}}` becomes a `nodeinfo` column of type
`STRUCT(type VARCHAR)`. Three coordinated pieces make
`nodeinfo.type` filterable:

- `discoverStructSubPaths` probes each top-level column at `LoadFile`
  time via `typeof` + `struct_keys`, recursing into nested STRUCTs
  (capped at 6 levels), storing dotted paths in `tableStructPaths`.
- `buildUnionSelect` projects STRUCT columns through `to_json(col)`
  rather than `CAST AS VARCHAR` — DuckDB's struct-to-text cast emits
  `{'k': 'v'}` (single-quoted, **not** valid JSON), which downstream
  `json_extract_string` cannot parse.
- `quoteFieldRef` resolves a field name against literal columns first
  (so a column genuinely named `app.name` still works), then against
  `tableStructPaths` (emitting `json_extract_string("nodeinfo",
  '$.type')`), then falls back.

`GetFields()` and `FieldSamples` both return the union of literal
columns and struct paths.

## Frontend internals

- `src/hooks/useLocalStorage.js` — `[value, setValue]`, lazy read on
  mount, auto-persist on change, booleans as `'1'|'0'`.
- `src/utils/datetime.js` — the single home for date parsing and
  formatting: `DT_RE`, `isValidDateTime`, `isoToText`,
  `parseUserDateTime`, `formatTimestamp`, `formatTimeout`.
- `src/utils/queryDsl.js` — LogQL-shaped serialize/parse. Empty `{}`
  stream selector + `|=` line filters + `| field op "value"` label
  filters + an `@time:` extension line. Operator map: `is` → `=`,
  `is_not` → `!=`, `contains`/`wildcard` → `=~`, `exists` → `=~ ".+"`,
  `does_not_exist` → `= ""`, `is_one_of` → anchored regex alternation.
  Round-trip is lossless for the obs-viewer model; `contains` and
  `wildcard` both serialize as `=~` and parse back as `wildcard`.
- `LogTable` persists column widths under a key derived from the column
  set shape, so widths survive restarts but reset when the schema
  changes shape.
- Server reads all go through TanStack Query hooks in
  `hooks/useApiQueries.js` with a 5-min `staleTime`. Mutations
  invalidate via `queryClient.invalidateQueries`.

## Shutdown behaviour

Three layers, in order of directness:

1. **Explicit** — status-bar "Stop server" button POSTs
   `/api/shutdown`, which exits after a 2-second grace period. Any
   `/api/version` touch inside that window (a second tab still polling)
   cancels the shutdown.
2. **Tab close** — `pagehide` fires
   `navigator.sendBeacon('/api/shutdown')`. Best-effort, doesn't block
   tab close, multi-tab-safe via the same grace cancellation.
3. **Inactivity** — the `timeout` loop (default 180s) is the fallback
   for force-kill and browser-crash cases. The SPA's 30s
   `/api/version` poll keeps the timer fresh with ~6× margin while any
   tab is open. Modules registering an `AddBusyCheck` predicate pause
   the loop while they're mid-work.

## API Reference

All endpoints return JSON. Errors: `{"error": "message"}`.

| Method | Path | Body/Params | Returns |
|--------|------|-------------|---------|
| GET | `/api/version` | — | `{version, os, arch, idle_timeout_seconds}` |
| GET | `/api/files` | — | `{files[], timestamp_field}` |
| POST | `/api/files/load` | `{path}` | `{status}` |
| POST | `/api/files/unload` | `{id}` | `{status}` |
| POST | `/api/files/toggle` | `{id, enabled}` | `{status}` |
| POST | `/api/files/load-dir` | `{path}` | `{status, loaded[], errors[]}` |
| GET | `/api/browse` | `?path=` | `{current_path, entries[]}` |
| POST | `/api/query` | `{filters[], time_from, time_to, sort_order, search_text, offset, limit}` | `{rows[], total_count, fields[], offset, limit}` |
| GET | `/api/fields` | — | `{fields[]}` |
| GET | `/api/field-samples` | `?fields=a,b,c&cap=N` | `{field: [v1,…]}` — DISTINCT values per field, capped |
| GET | `/api/timerange` | — | `{min, max, timestamp_fields[]}` |
| GET/POST | `/api/timestamp-field` | POST: `{field}` | `{field}` |
| POST | `/api/export` | `{query, output_path}` | `{status, records, path}` |
| POST | `/api/export/self-copy` | `{target_dir}` | `{status, path}` |
| GET/POST | `/api/settings` | POST: `{last_directory, …}` | settings object / `{status}` |
| POST | `/api/shutdown` | — | exits after 2s grace |
| GET | `/api/modules` | — | `{modules: [{id, tab?, bundle?, style?, config?}]}` |
| GET | `/api/openapi.json` | — | Swagger 2.0 spec generated from `swag` annotations |

Modules add routes under `/api/<name>/*` and `/m/<name>/*`, which
appear only when the module's `[<name>]` config section is present.

## Module conventions

No modules ship by default. See [docs/MODULES.md](./docs/MODULES.md)
for the full contract. In brief:

- Each module lives under `modules/<name>/` with a Go-valid package
  name even when the directory has dashes.
- Modules with both halves split into `backend/` and `frontend/`.
  Data-only modules flatten — Go's `embed` cannot escape upward with
  `..`, so embedded files must sit beside the source.
- Implement `module.Module`: `Name()`, `Enabled(*config.Config)`,
  `Register(*RegisterContext)`. `Enabled` should consult
  `cfg.ModuleEnabled("<name>")` so a config section is the on/off
  switch.
- Routes go under `/api/<name>/*` and `/m/<name>/*`, disjoint from core
  paths so longest-prefix matching keeps them clear of the SPA catch-all.
- Register the React component in `frontend/src/moduleRegistry.js` and
  add a `resolve.alias` entry in `frontend/vite.config.js` for every
  external package the module imports — module JSX sits outside the
  Vite project root, so Rollup's node_modules walk-up never reaches
  `frontend/node_modules`.

## Known Limitations / Backlog

- **Free-text search** uses `TRY_CAST(combined.* AS VARCHAR) ILIKE`,
  which is DuckDB-specific and may misbehave on exotic column types; a
  per-column union approach would be more robust.
- **No authentication** — fine for localhost, needs an auth layer
  before any network exposure.
- **Trace visualization** — the table view works for logs and metrics;
  traces would benefit from a Gantt/waterfall span view. Design and
  cost tradeoffs are in [docs/TRACE_VIEW_PROPOSAL.md](./docs/TRACE_VIEW_PROPOSAL.md).
- **Per-module bundle splitting** — module React code is bundled with
  the main Vite output because `moduleRegistry.js` imports statically.
  The Manifest already exposes an optional `Bundle` URL field so a
  split build can arrive without changing the contract.
- **API client migration** — `frontend/src/api/client.js` and
  `frontend/src/api/generated/` coexist; migration is per-component and
  ungated.
- **NDJSON schema inference is capped** at `sample_size=50000`. Fields
  that appear *only* beyond row 50,000 are silently dropped. This is a
  deliberate load-time tradeoff (~37% faster on a 200 MB file); raise
  the constant in `engine.go` if your data is non-uniform.
- **Swag CLI** — `swag init` runs from `build.sh` only if the CLI is on
  PATH. Builds without it succeed using the committed `swagger.json`.

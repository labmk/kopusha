# obs-viewer

**Makes unstructured logs queryable without writing extraction code.**

Point it at a folder of logs that share no schema — NDJSON here, a
bracketed text format there, Windows event logs, XML — and get one
queryable table with a real filter UI over it. No ingestion step, no
index, no server, nothing to install.

obs-viewer embeds a [DuckDB](https://duckdb.org) query engine and a
React UI into one executable.

```
obs_viewer --dir ./logs/
```

## Why

Querying local files with SQL is a solved problem, and there are good
tools for it. They all assume your data is already structured: Parquet,
CSV, JSON.

Logs are not. A line like

```
2026-03-18 06:00:00.000 [INF] [SCH] [PID:4179] queue depth 2347 exceeds limit
```

is one string column to every one of those tools. Making it queryable
means hand-writing regex extraction in SQL, per format, and then
hand-writing the union across files whose fields don't line up.

That's the part obs-viewer does. Formats are described by dropping a
YAML rule into `parsers.d/` — no recompile — and files with unrelated
schemas load together into one union you can filter across. The query
UI is there because the people who need this are not the people who
enjoy writing regex; those already have grep.

The deployment cost is one file you can copy onto an air-gapped
machine.

## Features

- **Single binary.** Frontend, query engine, and DuckDB extensions are
  compiled in. No runtime dependencies. Nothing is fetched to run: the
  only outbound request is an optional startup check for a newer release,
  which reports and never installs — turn it off with `update_check =
  false` and the binary makes no network calls at all.
- **Multi-format ingest.** NDJSON, Windows EVTX, XML, and line- or
  block-structured text logs. New text formats are added by dropping a
  YAML rule into `parsers.d/` — no recompile.
- **Heterogeneous schemas.** Load files with completely different
  fields at once. Queries union them and fill `NULL` for columns a
  given file lacks.
- **Nested field filtering.** JSON objects materialise as DuckDB
  `STRUCT`s; a filter on `nodeinfo.type` resolves to the sub-path, and
  the field picker surfaces those alongside flat columns.
- **Query builder + text DSL.** A visual filter builder with 10
  operators, plus a pipeline-style text view for copy/paste portability.
- **Time filtering with explicit UTC.** Presets, custom ranges, and a
  UTC-offset timezone selector that avoids DST ambiguity.
- **Result table.** Expandable rows, per-schema persisted column widths,
  page size up to 10,000 rows. Rows are paged, not virtualised — a full
  10,000-row page is a lot of DOM, so reach for a smaller page size or a
  tighter filter if it feels heavy.
- **Export.** Write the current filtered view back out as NDJSON or
  Parquet — Parquet keeps the types and is typically ~10x smaller, so
  the result drops straight into any other analytical tool. Optionally
  copy the binary alongside it so the recipient needs no setup at all.
- **Localhost by default.** Binds `127.0.0.1` unless you both set
  `listen=` and supply a TLS certificate.

## Usage

```bash
obs_viewer                                            # port 9200, opens a browser
obs_viewer --dir /path/to/logs/                       # pre-load a directory
obs_viewer --files "/path/to/*.ndjson"                # pre-load a glob
obs_viewer --port 8080 --no-browser                   # custom port, no auto-open
obs_viewer --port 9443 --cert cert.pem --key key.pem  # TLS
obs_viewer --verbose                                  # startup phase timings
obs_viewer --no-update-check                          # skip the release check
```

Keep the `parsers.d/` folder beside the binary. The server exits on its
own once no browser tab has polled it for `timeout` seconds (default
180), so a forgotten process doesn't linger.

## Supported formats

| Format | Detection | Notes |
|--------|-----------|-------|
| NDJSON | `.ndjson`, or first line parses as a JSON object | Direct DuckDB path — original schema preserved as written |
| Parquet | `PAR1` magic bytes, or `.parquet`/`.pq` | Direct DuckDB path — already typed, so nothing is inferred |
| EVTX | `ElfFile\0` magic bytes | Windows event logs |
| XML | First non-whitespace char is `<` | Row element autodetected; dot-path flattened, `@`-prefixed attributes |
| Block records | A line of 20+ dashes near the start | `Key: Value` fields between separators |
| Line logs | YAML regex rules in `parsers.d/` | 5 shipped variants; add your own |

Every non-NDJSON record gets an injected `@timestamp` (ISO-8601 UTC)
and `_source_format`. See [REQUIREMENTS.md](./REQUIREMENTS.md) for the
full per-format contract and the test matrix that enforces it.

### Adding a text format

Drop a YAML file in `parsers.d/` and restart:

```yaml
family: line
name: my-format
priority: 90
parse: '^(?P<ts>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\s+(?P<level>\w+)\s+(?P<message>.*)$'
ts_layout: '2006-01-02 15:04:05'
```

Rules describe structure only — regexes and field paths, no product
knowledge. Files load in lexicographic order, so a `00-` prefix takes
precedence over `50-`.

### When a rule isn't enough

A YAML rule covers any format whose structure a regex can describe. Some
cannot be: binary formats, anything needing a decompression or decoding
library, or a grammar that regular expressions genuinely can't express.
Those need a new adapter in Go, and possibly a new dependency — a change
to the binary rather than to a config file.

**That's a welcome contribution.** Open a pull request, or an issue if
you'd rather discuss the shape first, with:

- **A description of the format** — what delimits a record, where the
  timestamp is and in what layout, which fields are fixed and which are
  optional or variable-width.
- **An anonymized sample.** Retype the shape with invented values rather
  than sending real log lines. Anything attached to a public repository
  is public permanently, and a trimmed real log still carries whatever is
  embedded in its message bodies.
- **Why a `parsers.d/` rule can't do it** — briefly. It's the first thing
  that will be asked, and it's often worth a second look.

Any new dependency has to be permissive-licensed; see
[CONTRIBUTING.md](./CONTRIBUTING.md) for that constraint and for the
adapter checklist.

## Configuration

`obs_viewer.conf` sits next to the binary. Every value is optional; the
shipped file has all of them commented out.

```ini
port    = 9200       # HTTP port
timeout = 180        # inactivity auto-shutdown, seconds; 0 disables
listen  = 127.0.0.1  # non-loopback requires --cert/--key
parsers_dir = parsers.d
update_check = true  # check GitHub for a newer release at startup
```

All `obs_viewer*.conf` files in the directory are merged at startup in
alphabetical order, so a module can ship its own sibling file.
`.example` suffixes are deliberately skipped.

## Build

Requires Go 1.25.6+, Node 20+, and a C compiler (DuckDB needs CGO).

```bash
./build.sh                             # host platform
GOOS=linux ./build.sh                  # Linux amd64
GOOS=darwin GOARCH=arm64 ./build.sh    # macOS Apple Silicon
VERSION=1.0.0 ./build.sh               # custom version
MANUFACTURER="Acme Corp" ./build.sh    # brand a fork
```

Output lands in `dist/` alongside `parsers.d/` and `obs_viewer.conf`.

Windows binaries are produced by CI; that is the supported path. A
local Windows build needs a MinGW-w64 GCC whose C++ ABI matches the
prebuilt `libduckdb_static.a` — `build.sh` probes for one and prints
which it picked. Which toolchains currently work, and the
cross-compilation notes for every target, are in
[docs/BUILD.md](./docs/BUILD.md).

## Development

[ARCHITECTURE.md](./ARCHITECTURE.md) is the map — layers, contracts,
and the decisions that are load-bearing. [CONTRIBUTING.md](./CONTRIBUTING.md)
covers the change workflow.

```bash
go run .                          # backend on :9200
cd frontend && npm run dev        # Vite on :5173, proxies /api to :9200
cd frontend && npm run test:e2e   # Playwright (needs dist/ built first)
go test ./...                     # Go suite
```

The OpenAPI spec is generated from `swag` annotations on the handlers
and is the source of truth for the typed TypeScript client:

```bash
swag init -g main.go --output internal/server/docs --parseInternal --parseDependency
cd frontend && npm run gen:api
```

## Extending

obs-viewer has a module system for optional sub-features that ship
their own Go handlers, React tab, and static assets. No modules ship by
default — see [docs/MODULES.md](./docs/MODULES.md) for the contract and
a worked example.

## Roadmap

Direction and the reasoning behind it: [docs/ROADMAP.md](./docs/ROADMAP.md).
Tracking is in [issues](https://github.com/labmk/obs-viewer/issues),
grouped by milestone.

## Security

- **No authentication.** The loopback-only default is what keeps it
  safe. Do not expose it on a network without an authenticating proxy
  in front.
- Non-loopback `listen` values are ignored unless TLS is configured.
- The release check is the only outbound request, sends nothing about
  the host, and never downloads or installs. `update_check = false`
  removes it.
- Report vulnerabilities per [SECURITY.md](./SECURITY.md).

## AI-supported development

obs-viewer was written with AI assistance. Architecture, design
decisions, review and testing are human-owned; a substantial share of
the implementation, documentation and test-fixture generation was
Treat this the way you would any other code you did not write: read it
before you run it on data you care about.

## License

MIT — see [LICENSE](./LICENSE). Copyright (c) 2026 labmk.

Everything compiled into the binary is permissive — MIT, BSD, Apache-2.0,
ISC. [THIRD-PARTY-NOTICES.md](./THIRD-PARTY-NOTICES.md) lists it, and
`sbom.cdx.json` is the machine-readable CycloneDX equivalent, shipped
with every release.

obs-viewer is an independent open source project. Anyone is free to
use, modify and redistribute it, including commercially, under the MIT
terms. It is provided as-is, with no warranty and no support
commitment.

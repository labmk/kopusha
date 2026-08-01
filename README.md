# obs-viewer

A single-binary, local-first viewer for log, metric and trace files.
Point it at a folder, open a browser tab, and query gigabytes of
heterogeneous log data at SQL speed — no server, no index, no
infrastructure.

obs-viewer embeds a [DuckDB](https://duckdb.org) query engine and a
React UI into one executable. There is nothing to install and nothing
to configure to get started.

```
obs_viewer --dir ./logs/
```

## Why

Log triage usually means one of two bad options: grep across a pile of
files that don't share a schema, or stand up an OpenSearch/Loki stack
to answer one question. obs-viewer is the middle path — the ergonomics
of a real query UI, with the deployment cost of a single file you can
copy onto an air-gapped machine.

## Features

- **Single binary.** Frontend, query engine, and DuckDB extensions are
  compiled in. No runtime dependencies, no network access required.
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
  operators, plus a LogQL-shaped text view for copy/paste portability.
- **Time filtering with explicit UTC.** Presets, custom ranges, and a
  UTC-offset timezone selector that avoids DST ambiguity.
- **Virtualised table.** Expandable rows, per-schema persisted column
  widths, page size up to 10,000 rows.
- **Export.** Write the current filtered view back out as NDJSON, and
  optionally copy the binary alongside it so the recipient can open the
  result with no setup at all.
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
```

Keep the `parsers.d/` folder beside the binary. The server exits on its
own once no browser tab has polled it for `timeout` seconds (default
180), so a forgotten process doesn't linger.

## Supported formats

| Format | Detection | Notes |
|--------|-----------|-------|
| NDJSON | `.ndjson`, or first line parses as a JSON object | Direct DuckDB path — original schema preserved as written |
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

## Configuration

`obs_viewer.conf` sits next to the binary. Every value is optional; the
shipped file has all of them commented out.

```ini
port    = 9200       # HTTP port
timeout = 180        # inactivity auto-shutdown, seconds; 0 disables
listen  = 127.0.0.1  # non-loopback requires --cert/--key
parsers_dir = parsers.d
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

## Security

- **No authentication.** The loopback-only default is what keeps it
  safe. Do not expose it on a network without an authenticating proxy
  in front.
- Non-loopback `listen` values are ignored unless TLS is configured.
- Report vulnerabilities per [SECURITY.md](./SECURITY.md).

## AI-supported development

obs-viewer was written with AI assistance (Claude Code). Architecture,
design decisions, review and testing are human-owned; a substantial
share of the implementation, documentation and test-fixture generation
was AI-produced under that review. [CLAUDE.md](./CLAUDE.md) is the
working brief that guided it.

Treat this the way you would any other code you did not write: read it
before you run it on data you care about.

## License

MIT — see [LICENSE](./LICENSE). Copyright (c) 2026 labmk.

obs-viewer is an independent open source project. Anyone is free to
use, modify and redistribute it, including commercially, under the MIT
terms. It is provided as-is, with no warranty and no support
commitment.

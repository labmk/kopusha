# Changelog

Notable changes to obs-viewer. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the version is `0.x`, minor bumps may carry breaking changes to
the HTTP API, the `parsers.d/` rule schema, and the module contract.
The file formats obs-viewer *reads* are not affected by that caveat —
those are external and stable.

## [Unreleased]

### Added

- Release notification. At startup obs-viewer checks the GitHub releases
  API and, if a newer version exists, the status bar links to its release
  page. `GET /api/update` exposes the result.

  It only ever reports — nothing is downloaded, nothing is installed, and
  the running binary is never modified. Signed, user-initiated updates
  are deferred; see [docs/SELF_UPDATE_PROPOSAL.md](./docs/SELF_UPDATE_PROPOSAL.md)
  for why replacing the binary needs release signing first.

  On by default. Turn it off with `update_check = false` in
  `obs_viewer.conf`, or `--no-update-check` for one run — after which
  obs-viewer makes no network requests at all. The check runs in the
  background, never delays startup, and fails silently, so an air-gapped
  host sees nothing.

### Changed

- EVTX parsing moved from `0xrawsec/golang-evtx` to
  `www.velocidex.com/golang/evtx`. The old parser is GPL-3.0, which an
  MIT-licensed binary cannot statically link; every dependency that
  reaches the binary is now permissive. It also panicked from inside its
  own goroutine on malformed binary XML — unrecoverable by the caller
  and fatal to the process — where the new parser returns errors.
- **Breaking (EVTX columns):** the event ID is now at
  `Event.System.EventID.Value` rather than `Event.System.EventID`, and
  `EventData` entries are keyed by name directly. Saved filters
  referencing the old paths need updating. `@timestamp`,
  `_source_format` and `Event.System.Channel`/`Computer` are unchanged.

### Fixed

- A truncated or corrupt EVTX chunk no longer aborts the whole file —
  readable events before the damage are still ingested, and a file where
  nothing parses reports the underlying error instead of appearing
  empty.

## [0.1.0] — unreleased

First public release.

### Added

- Single-binary log/metric/trace viewer: Go backend, embedded DuckDB
  query engine, React SPA bundled via `go:embed`. No runtime
  dependencies and no network access required.
- Ingest for NDJSON (direct DuckDB path), Windows EVTX, XML, and
  block- and line-structured text logs through a pluggable adapter
  layer with confidence-scored format detection.
- Rule-driven text parsing: new line/block/XML formats are added by
  dropping a YAML file into `parsers.d/`, with no recompile. Five line
  variants, one block variant, and a generic XML row-element rule ship
  by default.
- Heterogeneous schema queries — files with unrelated field sets load
  together, and queries union them with `NULL` fill.
- Nested field filtering: JSON objects materialise as DuckDB `STRUCT`s
  and their dotted sub-paths are filterable and appear in the field
  picker.
- Visual query builder with ten operators, plus a LogQL-shaped text DSL
  that round-trips with it.
- Time filtering with explicit UTC offsets, avoiding DST ambiguity.
- Virtualised result table with expandable rows and per-schema
  persisted column widths.
- NDJSON export, optionally copying the binary alongside the data so a
  recipient can open the result with no setup.
- Module system for optional sub-features with their own Go handlers,
  React tab, and assets. No modules ship by default.
- OpenAPI spec generated from `swag` annotations, driving a generated
  TypeScript client.

### Security

- Binds `127.0.0.1` by default. A non-loopback `listen` is honoured
  only when a TLS certificate is supplied; otherwise the server warns
  and falls back to loopback. There is no authentication layer — see
  [SECURITY.md](./SECURITY.md) for the threat model.
- DuckDB extensions are statically linked; obs-viewer never downloads
  an extension at runtime, which is what makes air-gapped use safe.

[Unreleased]: https://github.com/labmk/obs-viewer/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/labmk/obs-viewer/releases/tag/v0.1.0

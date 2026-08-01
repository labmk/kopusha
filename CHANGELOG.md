# Changelog

Notable changes to obs-viewer. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the version is `0.x`, minor bumps may carry breaking changes to
the HTTP API, the `parsers.d/` rule schema, and the module contract.
The file formats obs-viewer *reads* are not affected by that caveat —
those are external and stable.

## [Unreleased]

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

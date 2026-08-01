# Third-party notices

obs-viewer 0.1.0 is distributed as a single statically linked
executable. Every component listed here is compiled into that binary,
which is why their notices ship with it: MIT and BSD require the
copyright notice to travel with copies, and Apache-2.0 §4 requires
attribution.

obs-viewer's own code is MIT — see [LICENSE](./LICENSE). This file
covers everything else.

**Generated** by `scripts/generate-sbom.py`; do not edit by hand.
A machine-readable CycloneDX equivalent is in
[sbom.cdx.json](./sbom.cdx.json).

## Summary

| Licence | Components |
|---------|-----------|
| 0BSD | 1 |
| Apache-2.0 | 10 |
| BSD-2-Clause | 1 |
| BSD-3-Clause | 6 |
| ISC | 1 |
| MIT | 62 |
| PostgreSQL | 1 |

Every licence above is permissive. No component is under GPL, AGPL,
or any other reciprocal licence — a constraint enforced by policy in
[CONTRIBUTING.md](./CONTRIBUTING.md), because a copyleft dependency
would make the distributed binary undistributable under MIT.

## 0BSD

- `tslib` 2.8.1 — Runtime library for TypeScript helper functions

## Apache-2.0

- `github.com/Velocidex/ordereddict` v0.0.0-20210502082334-cf5d9045c0d1
- `github.com/go-openapi/jsonpointer` v0.19.5
- `github.com/go-openapi/jsonreference` v0.20.0
- `github.com/go-openapi/spec` v0.20.6
- `github.com/go-openapi/swag` v0.19.15
- `gopkg.in/yaml.v2` v2.4.0
- `gopkg.in/yaml.v3` v3.0.1
- `org.duckdb.third_party/fastpforlib` — Integer compression (FastPFor)
- `org.duckdb.third_party/mbedtls` — Crypto primitives
- `www.velocidex.com/golang/evtx` v0.2.0

## BSD-2-Clause

- `github.com/pkg/errors` v0.8.1

## BSD-3-Clause

- `github.com/google/uuid` v1.6.0
- `golang.org/x/mod` v0.32.0
- `golang.org/x/sync` v0.19.0
- `golang.org/x/tools` v0.41.0
- `org.duckdb.third_party/re2` — Regular expressions
- `org.duckdb.third_party/zstd` — Zstandard compression (dual BSD-3/GPL-2; BSD taken)

## ISC

- `github.com/davecgh/go-spew` v1.1.2-0.20180830191138-d8f796af33cc

## MIT

- `@babel/runtime` 7.29.2 — babel's modular runtime helpers
- `@floating-ui/core` 1.7.5 — Positioning library for floating elements: tooltips, popovers, dropdowns, and more
- `@floating-ui/dom` 1.7.6 — Floating UI for the web
- `@floating-ui/react-dom` 2.1.8 — Floating UI for React DOM
- `@floating-ui/utils` 0.2.11 — Utilities for Floating UI
- `@radix-ui/primitive` 1.1.3
- `@radix-ui/react-compose-refs` 1.1.2
- `@radix-ui/react-context` 1.1.2
- `@radix-ui/react-dialog` 1.1.15
- `@radix-ui/react-focus-guards` 1.1.3
- `@radix-ui/react-id` 1.1.1
- `@radix-ui/react-popover` 1.1.15
- `@radix-ui/react-slot` 1.2.3
- `@radix-ui/react-use-callback-ref` 1.1.1
- `@radix-ui/react-use-controllable-state` 1.2.2
- `@radix-ui/react-use-effect-event` 0.0.2
- `@radix-ui/react-use-escape-keydown` 1.1.1
- `@radix-ui/react-use-layout-effect` 1.1.1
- `@radix-ui/react-use-rect` 1.1.1
- `@radix-ui/react-use-size` 1.1.1
- `@radix-ui/rect` 1.1.1
- `@tanstack/query-core` 5.100.13 — The framework agnostic core that powers TanStack Query
- `@tanstack/react-query` 5.100.13 — Hooks for managing, caching and syncing asynchronous and remote data in React
- `@types/prop-types` 15.7.15 — TypeScript definitions for prop-types
- `@types/react` 18.3.28 — TypeScript definitions for react
- `aria-hidden` 1.2.6 — Cast aria-hidden to everything, except...
- `csstype` 3.2.3 — Strict TypeScript and Flow types for style based on MDN data
- `detect-node-es` 1.1.0 — Detect Node.JS (as opposite to browser environment). ESM modification
- `get-nonce` 1.0.1 — returns nonce
- `github.com/KyleBanks/depth` v1.2.1
- `github.com/duckdb/duckdb-go-bindings` v0.10505.0
- `github.com/duckdb/duckdb-go-bindings/lib/darwin-arm64` v0.10505.0
- `github.com/duckdb/duckdb-go/v2` v2.10505.0
- `github.com/go-viper/mapstructure/v2` v2.5.0
- `github.com/josharian/intern` v1.0.0
- `github.com/mailru/easyjson` v0.7.6
- `github.com/swaggo/swag` v1.16.6
- `js-tokens` 4.0.0 — A regex that tokenizes JavaScript.
- `loose-envify` 1.4.0 — Fast (and loose) selective `process.env` replacer using js-tokens instead of an AST
- `memoize-one` 5.2.1 — A memoization library which only remembers the latest invocation
- `org.duckdb.third_party/fmt` — Formatting library
- `org.duckdb.third_party/fsst` — Fast static symbol table string compression
- `org.duckdb.third_party/hyperloglog` — Cardinality estimation
- `org.duckdb.third_party/miniz` — Deflate/inflate
- `org.duckdb.third_party/skiplistlib` — Skip list
- `org.duckdb.third_party/utf8proc` — Unicode normalisation
- `org.duckdb.third_party/yyjson` — JSON parsing
- `org.duckdb/duckdb` — Embedded analytical database engine
- `org.duckdb/duckdb-extension-autocomplete` — SQL autocomplete
- `org.duckdb/duckdb-extension-core-functions` — Core scalar functions
- `org.duckdb/duckdb-extension-icu` — ICU / timezone extension
- `org.duckdb/duckdb-extension-json` — JSON extension
- `org.duckdb/duckdb-extension-parquet` — Parquet extension
- `react` 18.3.1 — React is a JavaScript library for building user interfaces.
- `react-dom` 18.3.1 — React package for working with the DOM.
- `react-remove-scroll` 2.7.2 — Disables scroll outside of `children` node.
- `react-remove-scroll-bar` 2.3.8 — Removes body scroll without content _shake_
- `react-style-singleton` 2.2.3 — Just create a single stylesheet...
- `react-window` 1.8.11 — React components for efficiently rendering large, scrollable lists and tabular data
- `scheduler` 0.23.2 — Cooperative scheduler for the browser environment.
- `use-callback-ref` 1.3.3 — The same useRef, but with callback
- `use-sidecar` 1.1.3 — Sidecar code splitting utils

## PostgreSQL

- `org.duckdb.third_party/pg_query` — PostgreSQL-derived SQL parser (libpg_query)

## A note on the DuckDB components

`libduckdb_static.a` arrives prebuilt from `duckdb-go-bindings`, so no
scanner can see inside it. The DuckDB entries above were derived from
the static archives the linker actually consumes and their licences
read from the DuckDB source tree. They are marked in the SBOM with an
`obs-viewer:evidence` property recording that provenance, rather than
being presented as scanner output.

Full licence texts are available from each project's repository. The
DuckDB bundle's texts ship inside the `duckdb-go-bindings` module.

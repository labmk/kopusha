# Contributing

kopusha has one maintainer and is not anyone's day job. Responses are
best effort.

- **Bug reports** are welcome, especially with a file that reproduces
  the problem. The file must be synthetic or fully redacted; see
  [Test data policy](#test-data-policy).
- **Small fixes** (a parser rule, a regex, a UI problem) can go
  straight to a pull request.
- **Large changes** (a new ingest family, a schema change, anything in
  the query pipeline) should start as an issue, so the work does not
  collide with something already in progress.
- **Security issues** go through [SECURITY.md](./SECURITY.md), not
  public issues.

## Setup

You need Go 1.26.6 or later with CGO enabled, Node 22.12 or later with
npm, and a C compiler. DuckDB is a CGO dependency; there is no pure-Go
build. Per-platform compiler notes are in [docs/BUILD.md](./docs/BUILD.md);
if linking fails with missing symbols or a linker crash, look there
first.

```bash
./build.sh                        # full build: UI and binary into dist/
./dist/kopusha                    # run the build (dist\kopusha.exe on Windows)

go run .                          # backend only, on port 9200
cd frontend && npm run dev        # Vite dev server on 5173, forwards /api to 9200
```

Under `go run`, the executable lives in a temporary build folder, so
`kopusha.conf` and `parsers.d/` are not found: only NDJSON, Parquet and
EVTX load, and the embedded UI is whatever is currently in `static/`.
Use `./build.sh` and `dist/` to test parser rules or configuration.

After changing an HTTP handler's `swag` annotations, regenerate the
OpenAPI spec and the TypeScript client (`build.sh` does both when
`swag` is installed):

```bash
swag init -g main.go --output internal/server/docs --parseInternal --parseDependency
cd frontend && npm run gen:api
```

## Tests

```bash
go test ./...                     # Go tests
cd frontend && npm run test:e2e   # Playwright; needs dist/ built first
```

The e2e suite starts the real binary from `dist/` on port 9201 with
`test-fixtures/ndjson/` pre-loaded, so run `./build.sh` at least once
before it.

CI (`.github/workflows/build.yml`) runs `gofmt`, `go vet`, the Go tests
on every platform, the e2e tests on Linux and Windows, `govulncheck`
and `npm audit`.

## Pull request checklist

1. `go test ./...` and the e2e suite pass.
2. `gofmt` reports nothing.
3. New behaviour has a test. A new ingest format needs a REQ-DT row, a
   fixture, a Go unit test and an e2e test; see
   [REQUIREMENTS.md](./REQUIREMENTS.md#adding-a-format).
4. Regenerated files are in the same commit as the change that caused
   them (see [Generated files](#generated-files)).
5. User-visible changes bump the version (see [Releases](#releases)).

Commit messages explain why the change was made, not what the diff
shows. Long messages are fine.

To record that a commit was AI-assisted, use this neutral trailer:

```
Assisted-by: AI coding assistant
```

Do not name a vendor, product or model in commit messages, code
comments or documentation. Tool names change; git history cannot be
corrected later. Commits before v0.1.0 name a specific tool; that was
the earlier convention, and published history is not rewritten.

## AI-assisted contributions

AI tools are allowed and were used to write parts of this project. The
rule is about accountability: you are the author of what you submit.

- You can explain every line and why it is there.
- You ran the tests.
- You checked that the change does what its description says.
  Generated code is often plausible but wrong, and that is easy to miss
  when skimming.
- "The model wrote it" is not an answer to a review comment.

A pull request is closed without review if the author cannot explain
it, or if it contains invented facts: APIs, config keys, licences,
DuckDB behaviour or toolchain claims that nobody checked. This project
has lost two CI runs to inherited Windows build instructions that were
wrong.

You do not have to disclose tool use. You do have to stand behind the
result.

## Test data policy

This rule has no exceptions. Fixtures describe the structure a parser
must handle and never contain real captured data. No real host names,
user names, IP addresses (use the RFC 5737 ranges), file paths from a
real system, vendor or product names, or identifiers from a live
system.

Some fixtures from `test-fixtures/formats/` are copied into
`dist/samples/` and shipped in every release archive (see
[REQUIREMENTS.md](./REQUIREMENTS.md#fixtures-in-release-archives)).
Write every fixture as if strangers will download it.

Do not make a fixture by shortening a real log. Removing lines does not
remove what is inside the message text. Write it by hand, or add a
writer to `test-fixtures/generate.py` and regenerate.

The same applies to files attached to issues: attach only what you
accept being public permanently.

## Adding a log format

Most formats need no Go code. A line, block or XML format is a YAML
rule in `parsers.d/`. Build it in the UI or write it by hand; the rule
keys are in [ARCHITECTURE.md](./ARCHITECTURE.md#parser-rules-parsersd).
Rules describe text structure only (regexes, paths, field names), not
whose logs they came from.

Changing a shipped rule changes `parsers.d.sha256`, the manifest of the
rules a binary ships with. `./build.sh` regenerates it, or run:

```bash
go run ./cmd/genmanifest
```

A test fails if the committed manifest is out of date. A wrong manifest
would make self-update misjudge which rules the user edited.

Some formats cannot be described with a regular expression: binary
formats, compressed or encoded data, or grammars regexes cannot
express. These need a new adapter in `internal/ingest/<name>/` and
possibly a new dependency. The adapter interfaces are in
[ARCHITECTURE.md](./ARCHITECTURE.md#adapter-interfaces) and the
checklist is in [REQUIREMENTS.md](./REQUIREMENTS.md#adding-a-format).
If you propose one in an issue, include:

- a description of the format: what separates records, where the
  timestamp is and its layout, which fields are fixed and which are
  optional or variable width;
- an anonymised sample, retyped with invented values;
- a short reason why a `parsers.d/` rule cannot do it.

## Dependencies

The project is MIT licensed and every dependency must be permissive:
MIT, BSD, Apache-2.0, ISC, or at most MPL-2.0. GPL and AGPL dependencies
cannot be accepted, because the binary links statically and would
become a copyleft work.

The backend uses only the Go standard library apart from DuckDB, the
EVTX parser, YAML and swag. Any new dependency needs a reason.

After adding or upgrading a dependency, regenerate the notices and the
SBOM, check the licence summary for anything new, and commit the
result:

```bash
go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest   # once
python3 scripts/generate-sbom.py
```

## Code conventions

- **Go:** standard library HTTP, no frameworks. Core handlers in
  `internal/server/server.go`, query logic in `internal/engine/engine.go`,
  module handlers in `modules/<name>/`.
- **DuckDB queries:** case-insensitive matching with `ILIKE`; `*` in
  user input becomes `%`; SQL strings are escaped by doubling single
  quotes.
- **Engine schema** is cached when a file is loaded. Do not read
  `information_schema` on the query path.
- **Frontend state** lives in `App.jsx`. With Auto Apply on, filter,
  time and text changes re-run the query after 100 ms.
- **`localStorage`** is accessed through `useLocalStorage`, not raw
  `getItem` / `setItem`.
- **Dates** are parsed and formatted only in `utils/datetime.js`.
- **CSS** uses custom properties for theming. The defaults are neutral
  slate; a module may override `--accent` and `--accent-*`.

## Generated files

| File | Regenerate with |
|------|-----------------|
| `parsers.d.sha256` | `go run ./cmd/genmanifest` (or `./build.sh`) |
| `sbom.cdx.json`, `THIRD-PARTY-NOTICES.md` | `python3 scripts/generate-sbom.py` |
| `internal/server/docs/` | `swag init` (see [Setup](#setup)), also run by `./build.sh` |
| `frontend/src/api/generated/` | `npm run gen:api`, also run by `npm run build` |

Neither the SBOM nor the notices are edited by hand.

## Releases

A user-visible change bumps the version in four places that must match:

- `build.sh`: `VERSION`
- `frontend/package.json`: `version`
- `main.go`: the `version` variable and the `@version` swag annotation

The OpenAPI files in `internal/server/docs/` take the version from the
annotation the next time `swag init` runs.

## License

Contributions are licensed under the project's MIT license. There is no
CLA and no copyright assignment; you keep your copyright.

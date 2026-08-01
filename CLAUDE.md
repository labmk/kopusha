# CLAUDE.md

Guidance for Claude Code (claude.ai/code) when working in this
repository.

## Orientation

Read these first — they are the source of truth, and this file does not
duplicate them:

| Document | What it covers |
|----------|----------------|
| [ARCHITECTURE.md](./ARCHITECTURE.md) | Layout, layers, the ingest and module contracts, engine and frontend internals, API reference, known limitations |
| [REQUIREMENTS.md](./REQUIREMENTS.md) | Accepted data types, the per-format ingest contract, the fixture and test matrix |
| [CONTRIBUTING.md](./CONTRIBUTING.md) | Build, test, and change workflow |
| [README.md](./README.md) | What the tool is and how it is used |

In one line: a single static binary — Go backend, embedded DuckDB,
React SPA via `go:embed` — that reads log files off the local
filesystem and serves a query UI on localhost.

## Test data policy

**Fixtures must be synthetic.** `test-fixtures/` exercises format
grammar only — the shape a parser has to handle — never real captured
data. A fixture must not contain:

- real hostnames, usernames, IP addresses (use RFC 5737 ranges), or
  domain names other than `example.com`/`example.org`
- real filesystem paths from a specific deployment
- vendor or product names, real or invented-but-plausible
- identifiers from any real system: DICOM UIDs, patient/case IDs,
  serial numbers, correlation IDs copied from a live trace

Write fixtures by hand or generate them with `test-fixtures/generate.py`.
Do not trim a real log file down — trimming does not remove what is
embedded in the message bodies.

The single exception is `test-fixtures/formats/sample.evtx`, vendored
from Velocidex/evtx because the binary format cannot be synthesized;
see REQUIREMENTS.md for why that satisfies the policy. Do not add a
second exception without the same reasoning.

## Parser rule policy

Rules in `parsers.d/` **describe structure only** — regexes, dot-paths,
field names. No product names, hostnames, or deployment specifics. A
rule should be readable as a statement about text shape, not about
whose logs it came from.

## Code conventions

- Go: stdlib HTTP, no frameworks. Core handlers in `server.go`,
  business logic in `engine.go`, module handlers under `modules/<name>/`.
- DuckDB queries use `ILIKE` for case-insensitive matching; `*` is
  translated to SQL `%`. SQL string escaping is single-quote doubling.
- Engine schema is cached at `LoadFile` time — never re-read
  `information_schema` from the hot path.
- Frontend state lives in `App.jsx`; auto-query on filter/time/sort
  change with 100ms debounce, gated by the Auto Apply toggle.
- `localStorage` access goes through `useLocalStorage` — don't sprinkle
  raw `getItem`/`setItem` calls.
- Date parsing/formatting lives in `utils/datetime.js`; components must
  not define their own.
- CSS uses custom properties for theming. Core defaults are neutral
  slate; a branding module may override `--accent` / `--accent-*`.

## Release chores

Every change bumps the patch version in **four** places, which must
stay in sync:

- `build.sh` (`VERSION`)
- `frontend/package.json` (`version`)
- `main.go` — both the `version` var and the `@version` swag annotation

The generated `internal/server/docs/` files pick the version up from
the swag annotation on the next `swag init`.

## Licensing

The project is MIT. Every dependency must be permissive — MIT, BSD,
Apache-2.0, ISC, MPL-2.0 at the outside. **A GPL- or AGPL-licensed
dependency cannot be added**: static linking would make the distributed
binary a copyleft work, which the MIT license on this repository cannot
carry. Check the license before introducing a new module, and add it to
`THIRD-PARTY-NOTICES.md`.

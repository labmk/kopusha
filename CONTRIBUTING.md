# Contributing

Thanks for taking an interest. obs-viewer is a small, single-maintainer
project, so a quick note on expectations before you invest time:

- **Bug reports are always welcome**, especially ones with a file that
  reproduces the problem (redacted or synthesized — see below).
- **Small fixes** — a parser rule, a wrong regex, a UI papercut — can go
  straight to a pull request.
- **Large changes** are worth an issue first. A new ingest family, a
  schema change, or anything touching the query pipeline is a
  conversation before it is a diff, mostly so you don't build something
  that collides with work already in flight.
- Response times are best-effort. This is not anyone's day job.

## Getting set up

You need Go 1.25.6+ with CGO enabled, Node 20+, and a C compiler —
DuckDB is a CGO dependency, so there is no pure-Go path.

```bash
./build.sh                        # full build: frontend + Go binary into dist/
go run .                          # backend only, :9200
cd frontend && npm run dev        # Vite on :5173, proxies /api to :9200
```

Platform-specific toolchain notes — in particular which Windows C
compilers link against the prebuilt DuckDB archive — are in
[docs/BUILD.md](./docs/BUILD.md). If the link step fails with missing
symbols or a linker crash, that document is almost certainly the
answer.

## Running the tests

```bash
go test ./...                     # Go suite
cd frontend && npm run test:e2e   # Playwright — needs dist/ built first
```

The e2e suite spawns the real binary against `test-fixtures/`, so
`./build.sh` has to have run at least once.

## Before you open a pull request

1. `go test ./...` and the e2e suite pass.
2. `gofmt` clean.
3. New behaviour has a test. New *ingest formats* have more than that —
   see the checklist in [REQUIREMENTS.md](./REQUIREMENTS.md), which
   requires a REQ-DT row, a fixture, a Go unit test, and an e2e test.
   That matrix is the contract; a format without a row in it is not
   considered supported.
4. Version bumped in the places listed under "Release chores" in
   [CLAUDE.md](./CLAUDE.md), if the change is user-visible.

Commits should explain *why*, not restate the diff. Long-form is fine;
the existing history leans verbose deliberately.

## Test data policy

**This one is not negotiable.** Fixtures exercise format grammar only —
the shape a parser must handle — never real captured data. No real
hostnames, usernames, IP addresses (use the RFC 5737 ranges), filesystem
paths from a real deployment, vendor or product names, or identifiers
lifted from a live system.

Do not trim a real log file down to make a fixture. Trimming removes
rows; it does not remove what is embedded in the message bodies. Write
it by hand, or add a writer to `test-fixtures/generate.py` and
regenerate — the generator is seeded, so output is byte-reproducible.

The same applies to bug reports: if you attach a log, make sure it is
one you are willing to see in a public issue tracker forever.

## Dependencies

The project is MIT and every dependency must be permissive — MIT, BSD,
Apache-2.0, ISC, or MPL-2.0 at the outside. **A GPL- or AGPL-licensed
dependency cannot be accepted**, because static linking would make the
distributed binary a copyleft work that the MIT license on this
repository cannot carry.

After adding or upgrading a dependency, regenerate the notices and SBOM:

```bash
python3 scripts/generate-sbom.py
```

That rewrites both [THIRD-PARTY-NOTICES.md](./THIRD-PARTY-NOTICES.md)
and `sbom.cdx.json`; commit them with your change. Neither file is
edited by hand. It needs `cyclonedx-gomod` on `PATH`:

```bash
go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest
```

Check the output — if a new component lands under a licence not already
in the summary table, that is the moment to think about whether it
belongs here.

Beyond licensing, new dependencies need a reason. The backend is
deliberately stdlib-only apart from DuckDB, the EVTX parser, YAML, and
swag.

## Adding a log format

Most new formats need no Go at all. If the format is a line, block, or
XML shape, write a YAML rule in `parsers.d/` and it is picked up on
restart — see the example in the README and the existing rules for
reference. A genuinely new family (CSV, syslog, …) means a new adapter
under `internal/ingest/<name>/`; the `Loader` contract is documented in
[ARCHITECTURE.md](./ARCHITECTURE.md).

Parser rules describe **structure only**: regexes, dot-paths, field
names. A rule should read as a statement about text shape, not about
whose logs it came from.

## Security issues

Do not open a public issue. Follow [SECURITY.md](./SECURITY.md).

## License

By contributing, you agree that your contributions are licensed under
the MIT license that covers the project. There is no CLA and no
copyright assignment — you keep your copyright.

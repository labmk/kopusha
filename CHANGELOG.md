# Changelog

Notable changes to kopusha. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the version is `0.x`, minor bumps may carry breaking changes to
the HTTP API, the `parsers.d/` rule schema, and the module contract.
The file formats kopusha *reads* are not affected by that caveat —
those are external and stable.

## [Unreleased]

### Added

- **"Try the samples" in the empty state.** The sample logs have shipped
  since 0.3.1 with nothing in the interface pointing at them. One click
  now loads every parseable one — a log per supported format, queried
  together as a single table, which is the thing this tool exists to do
  and the hardest thing to explain without data.

  `unmatched.log` is loaded too, and fails, on purpose. The report names
  it and says where to look: *"9 of 10 loaded. unmatched.log did not
  parse — open it with + Add to see the diagnosis, or build a rule for it
  under Parser rules."* A first run that ends with a failure explained is
  worth more than one that hides it.

  New `GET /api/samples`. A binary copied without its `samples/` folder
  reports none available and the button simply does not appear.

- **A controls reference, behind `?` or the header button.** Every
  control and every keyboard shortcut, in one static panel.

  Deliberately not a first-run tour. A coach-mark overlay has to know
  where things are, which couples it to a layout that moves — and this
  release cycle has already moved the header twice. It also teaches at
  the moment the user has least context, then never again. A reference
  one keystroke away can be opened when the question actually occurs.

  `?` is ignored while typing, so it stays usable in a filter value.

### Changed

- The notice bar was inline-styled with a hardcoded colour and its own
  font size, which is how it escaped the 0.3.3 token sweep. It now uses
  the tokens like everything else.

## [0.3.3] — 2026-08-04

### Changed

- **The header is down to what it is for.** It carried a tab strip with a
  single option, styled as an active pill and centred like a title — a
  control with nothing to switch to. It now appears only when a module
  supplies a second tab, and the permanent tab is labelled `Parse`. The
  label it used to carry, `OBS Viewer`, was the last thing in the tree
  still holding the old name; the 0.3.0 rename matched `obs-viewer` and
  `obs_viewer` and never saw it.

- **Display preferences moved behind a settings button.** Timezone, time
  format and theme are set once and then left alone, so they were taking
  header room from things pressed constantly. They now live in a popover
  that closes on Escape or an outside click.

- **Hide null and Auto Apply moved into the query row.** Both change what
  the query returns, so they belong beside the filters rather than among
  the display preferences they used to sit with.

- **Copy link is gone.** The address bar already carries the full view —
  `writeState` has run on every change since 0.2.2 — so the button was a
  second way to do what selecting the URL does.

- **A type scale, instead of eight sizes.** Sizes ran 9, 10, 11, 12, 13,
  14, 15 and 32px with no system. Four tokens now cover the interface,
  and 9px and 10px are retired: they were carrying text people read, at
  contrast that could not support them.

### Fixed

- **Muted text now meets WCAG AA in both themes.** `--text-muted` scored
  3.79:1 on dark and **2.82:1 on light — an outright failure** — while
  carrying the histogram axis labels, the bar description and the status
  bar at 10–11px. It is now 5.97:1 and 5.20:1.

- **The light theme has visible structure.** Four near-identical greys
  (#EBEBEB, #F3F3F3, #F7F7F7, #FFFFFF) left panels indistinguishable at
  1.11:1. The app background is now 1.26:1 against panels, with borders
  darkened to match.

- **Per-file checkboxes announce which file they control.** They sat
  beside a `div` rather than a label, so a screen reader read only their
  state and every row sounded the same.

## [0.3.2] — 2026-08-04

### Fixed

- **Updating now brings `samples/` with it.** 0.3.1 added sample logs to
  the download, but the updater only knew about the binary and
  `parsers.d/` — so an install that updated from 0.3.0 got the new binary
  and no samples, and would have gone on missing every sample added
  later.

  Samples deliberately do *not* get the `parsers.d/` treatment. That
  policy exists because parser rules are configuration you are invited to
  edit, and losing an edit would lose real work. Samples are inert demo
  data that nobody hand-tunes, so carrying a second manifest to detect
  edits would be machinery bought for nothing. Shipped samples are simply
  written over.

  The folder is created when it is missing, which is the point: an
  install that came from a version predating `samples/` has none, and
  treating that absence as "you deleted it" would mean it never received
  any. The cost of that choice is that deleting the folder does not make
  it stay deleted — the update says so in its report.

  Anything in `samples/` that the release does not carry is left strictly
  alone, so it stays a folder you can keep your own files in.

## [0.3.1] — 2026-08-04

### Added

- **Sample logs in the download.** Every archive now carries a
  `samples/` folder — one synthetic log per supported format, the same
  fixtures the test suite runs against. A fresh install has something to
  open before anyone has to hand over a real log file, and the formats
  the tool exists to union are there to be loaded together.

  `samples/unmatched.log` is included precisely because it fails: it is
  what the "no rule matched" diagnosis and the rule builder open onto.

  The folder is inert — the binary never reads it and deleting it
  changes nothing — and it adds about 130 KB to an archive. Nothing in
  it came from a running system; the logs are generated by a seeded
  script and the addresses are RFC 5737 documentation ranges. The
  vendored EVTX fixture is the one thing not shipped, since it is
  third-party data rather than something this project generates.

## [0.3.0] — 2026-08-03

### Added

- **Update from inside the app.** When a newer release exists, the notice
  now offers to install it. Never automatic, never on a timer — only when
  someone presses the button, and never at all with
  `update_check = false`.

  It happens in two steps, and the middle one is the feature rather than
  a formality. *Prepare* downloads the archive, verifies it, and shows
  what installing would do — **without writing anything**. Only then does
  *Install and restart* touch the disk.

  What gets verified before a single byte is written: the downloaded
  bytes are hashed, and that digest is looked up in GitHub's attestations
  API. An archive no release workflow produced has no attestation and is
  refused. The attestation must also name this repository, the release
  workflow, the tag being installed, and a GitHub-hosted runner. This is
  not a full Sigstore signature check and is not described as one —
  SECURITY.md states exactly where the boundary is, and
  `gh attestation verify` remains the way to check it properly.

  **Your work is never overwritten.** Config files are not replaced at
  all. A parser rule is replaced only if it is byte-identical to the one
  this binary shipped with; anything you edited, added or deleted is left
  alone, and the update tells you which rules it skipped and why. A rule
  you deleted stays deleted, and a rule of your own is never displaced by
  a shipped one arriving under the same name.

  The previous binary is kept beside the new one as `.old`, so an update
  can be undone by renaming one file. It is swept away on the next start.

  On Unix the process re-executes itself and the page reconnects on its
  own. On Windows the update installs and the process exits — restarting
  there means racing for the listening port, and shipping an untested
  restart would be worse than asking for a double-click.

  Closes #17.

### Changed

- **The project is now called kopusha.** It was `obs-viewer`, a name that
  could not be found: every search for it returns broadcasting software
  or note-taking plugins, and `obs` silently stood for "observability",
  an expansion nobody could see. Renaming was cheapest now, before a
  self-updater started writing the old asset names into installs.

  What this changes on disk:

  | was | is |
  |---|---|
  | `obs_viewer` | `kopusha` |
  | `obs_viewer.conf`, `obs_viewer_<module>.conf` | `kopusha.conf`, `kopusha_<module>.conf` |
  | `obs_viewer_settings.json` | `kopusha_settings.json` |
  | `obs_viewer-<version>-<platform>.zip` | `kopusha-<version>-<platform>.zip` |
  | `github.com/labmk/obs-viewer` | `github.com/labmk/kopusha` |

  **Existing installs need their config and settings files renamed**; the
  old names are not read. GitHub redirects the old repository URL, and
  old release tags keep the assets they were published with.

  Every entry below this one predates the rename and describes files that
  were named `obs_viewer*` at the time. They have not been rewritten,
  because a release archive's name is a fact about a published artifact
  rather than a spelling choice — and the build attestations covering
  those archives are signed over the old names permanently.

## [0.2.2] — 2026-08-02

### Added

- **Signed build provenance on every release archive.** Each zip now
  carries a Sigstore attestation recording that those exact bytes were
  produced by this repository's workflow at a named commit:

  ```bash
  gh attestation verify kopusha-<version>-<platform>.zip --repo labmk/kopusha
  ```

  The certificate is issued to the workflow run and expires with it, so
  there is no signing key to store, leak or rotate.

  This closes a gap the 0.2.0 release notes could not: `SHA256SUMS`
  shows a download is intact against a list published in the same place
  by whoever published the file, which an attacker replacing the archive
  would replace too. Provenance is tied to the build rather than to the
  publisher.

  It is **not** code signing and does not silence Gatekeeper or
  SmartScreen — that needs a certificate authority vouching for a legal
  identity, and stays open as #24.
- **Field profiling.** A panel beside the results showing, per field:
  how many rows carry a usable value, an approximate distinct count,
  and how many of the loaded files declare the field at all. Expand a
  field for its most common values with their shares; click a value to
  filter to it.

  The hardest moment for someone new is not writing the query — the DSL
  is small and there is a builder for it. It is not knowing what the
  data contains. Until now the only way to find out was to load a file
  and read rows until you had a feel for it.

  Cost is kept where it belongs: nothing is computed until the panel is
  opened, the summary is a single scan covering every field at once,
  and value distributions are fetched per field on expand because each
  needs its own `GROUP BY`. New `POST /api/profile` and
  `POST /api/profile/values`.

  "Present" means the same thing here as the `exists` filter operator —
  not NULL and not empty — so clicking through lands on exactly the row
  count the panel showed.

## [0.2.1] — 2026-08-02

### Changed

- **A new release is now actually announced.** The check has worked
  since 0.1.0 and reported through a small link in the status bar — a
  fine place for a fact and a poor place for news. It went unnoticed
  through two releases.

  There is now a notice, once, with a link to the release page. It is
  not modal: kopusha gets opened to answer a question, and the
  release will still be there afterwards. Dismissing it is remembered
  per version, so declining 0.3.0 stays declined across restarts and
  says nothing about 0.4.0. The status-bar link stays either way —
  dismissing the interruption should not discard the information.

  It still says plainly that nothing self-installs, so nobody waits for
  an update that is never coming.
- The product label in the header links to the project page. The
  repository comes from `/api/version`, so a fork changes one Go
  constant rather than a string in the frontend.

## [0.2.0] — 2026-08-02

### Added

- **Time histogram.** A strip above the table showing record counts over
  time for the current filter. Drag across it to narrow the time range —
  it writes into the same time filter typing uses, and applies on its
  own, because a completed gesture should not need a confirming click.

  Bucket width is derived from the span of the *filtered* data, so
  narrowing to five minutes re-resolves to seconds instead of
  collapsing to one bar. The bar count is capped, which is what keeps
  an aggregate that runs on every query cheap. New `POST /api/histogram`.
- **Shareable views.** Filters, time range, sort, page size and hidden
  columns are encoded into the URL, and **Copy link** puts it on the
  clipboard. A refresh no longer loses the query.

  The state lives in the URL **fragment**, never the query string:
  filter values are log content — hostnames, user names, message
  fragments — and anything before the `#` reaches the server, its access
  log, and the `Referer` header of any outbound link. Loaded files are
  deliberately excluded; a path is meaningful only on the machine that
  produced it, so the recipient opens their own files and gets the
  sender's question applied to them.
- **Rule builder.** Paste sample lines and kopusha infers a
  `parsers.d/` rule — a regex with named captures, plus a Go time
  layout — then shows the rows it would produce before anything is
  written. Rename the fields, save, and the rule applies to the next
  load without a restart.

  The preview runs the candidate through the real `line` adapter over a
  temp file rather than reimplementing it, so it cannot disagree with
  what a load will produce.

  Until now the rule system — the part of this project with no
  substitute — was reachable only by hand-writing a Go regex into a
  YAML file from documentation, which is the exact skill the tool
  exists to make unnecessary.

  New endpoints: `GET /api/rules`, `POST /api/rules/suggest`,
  `POST /api/rules/preview`, `POST /api/rules/save`. New package
  `internal/parsers`, which now owns `parsers.d/` and the loader
  registry.
- **`POST /api/files/explain`.** When a file matches no rule, report
  every adapter's score *and* the reason it declined, the first
  non-blank line as the parser sees it, and any encoding trait that
  breaks matching invisibly — byte-order mark, CRLF, NUL bytes, invalid
  UTF-8. Read-only; it loads nothing.

  Shown automatically when a load fails, with a button that opens the
  rule builder seeded with that line. The scores already existed inside
  `Registry.Pick` and were discarded; the per-adapter reasons are new,
  supplied through the optional `Explainer` interface.
- **`ts_use_mtime_year`** in the `line` rule schema, for formats that
  carry month and day but no year — syslog's `Mar 18 06:00:00`. Those
  previously parsed to year 0, which sorted correctly and matched no
  time filter. Mutually exclusive with `ts_use_mtime_date`; setting
  both now fails rule compilation instead of resolving silently.
- **Parquet, read and write.** Export the current filtered view as
  Parquet, and load Parquet files back in. The DuckDB parquet extension
  is already statically linked, so this downloads nothing and works
  air-gapped. Format is chosen by the output extension.

  Measured on a 2,500-row fixture: 1,033,196 bytes as NDJSON,
  104,964 bytes as Parquet — about 10:1 — with types and nested
  structures surviving the round trip.

  New `REQ-DT-10` row in REQUIREMENTS.md, with a fixture generated by
  the export path itself.
- `DirectSQLIngester`, an extension to the loader contract for formats
  DuckDB reads with something other than `read_json_auto`.

### Changed

- **The result table is virtualised.** Only the rows in view are in the
  DOM. Measured: a 5,000-row page renders 50 row elements instead of
  5,000.
- **Row expansion moved to a side panel.** Clicking a row opens its full
  contents beside the table instead of expanding in place. Expanding
  in place pushed the following rows off screen, and — the reason it had
  to change — variable row heights make windowing impractical.

  The panel is a field list rather than a JSON blob, navigable with
  `j`/`k` or the arrow keys, closed with `Escape`.
- Release archives are **zip on every platform**. One format, one set of
  instructions.
- Removed references to other products throughout the repository.

### Fixed

- **Time-range bounds were compared as text.** Every column in the union
  is cast to VARCHAR, so a bound was matched lexicographically against
  the rendered form `2026-05-20 10:00:00` — meaning the same instant
  written `2026-05-20T10:00:00Z` sorted *after* it and the filter
  silently matched nothing. That held only while every bound came from
  one input widget emitting one shape. Bounds are now compared as
  timestamps, so any spelling of an instant selects the same rows.
- **Export wrote to a folder the user was no longer looking at.**
  Navigating the dialog's directory browser changed nothing unless
  **Select this folder** was clicked, so browsing somewhere and pressing
  Export wrote to the seeded path while the intended folder was on
  screen directly above it. The output path now tracks the folder being
  browsed, and the dialog opens in the last-used directory rather than
  the home directory. (#25)
- **Parquet was unreachable from the UI.** The engine chose the export
  format from the output extension, but the export dialog hard-coded
  `.ndjson` and never mentioned Parquet, and the file browser hid
  `.parquet` files so an exported file could not be browsed back to.
  Both halves of the round trip existed and neither could be clicked.
  The dialog now has a format selector driven by a single list, and
  `.parquet`/`.pq` are listed by the browser.
- The file browser closed after a failed load, discarding the error it
  had just set. It read its own `error` state through a stale closure.

## [0.1.1] — 2026-08-01

### Removed

- `react-window` as a dependency. It was declared but **never imported**
  — the virtualisation it implied was never built. Removing it also
  removes four packages from the SBOM (82 → 78 components).

### Fixed

- **Documentation overclaimed.** The README advertised a "Virtualised
  table" and ARCHITECTURE credited `react-window` for "virtual
  scrolling". Neither was true: every row of the current page is
  rendered. Both now say what the code does, and the missing
  virtualisation is recorded as a known limitation rather than an
  implicit promise.
- Pinned `typescript` to `^6`. The OpenAPI client generator declares an
  over-permissive peer range (`>=5.5.3`), so a transitive bump to
  TypeScript 7 satisfied it and then broke the build on an API that no
  longer exists. There is no newer generator release that handles 7.

### Changed

- Dependency updates within their existing ranges: Radix UI 1.1.15 →
  1.1.23, TanStack Query 5.100.13 → 5.101.4, Playwright 1.60.0 →
  1.62.1.
- Documentation consolidated: the agent brief was dissolved into
  CONTRIBUTING.md, which now carries house style and release chores.

React 19, Vite 8 and `@vitejs/plugin-react` 6 are available but not
taken — those are major upgrades, not dependency maintenance.

## [0.1.0] — 2026-08-01

First public release. There is no prior published version, so everything
here is new; the notes call out the decisions a reader is most likely to
want explained.

### Added

- Single-binary log/metric/trace viewer: Go backend, embedded DuckDB
  query engine, React SPA bundled via `go:embed`. No runtime
  dependencies.
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
- Visual query builder with ten operators, plus a pipeline-style text DSL
  that round-trips with it.
- Time filtering with explicit UTC offsets, avoiding DST ambiguity.
- Result table with expandable rows and per-schema persisted column
  widths. (This entry originally said "virtualised", which was never
  true — corrected in 0.1.1.)
- NDJSON export, optionally copying the binary alongside the data so a
  recipient can open the result with no setup.
- Module system for optional sub-features with their own Go handlers,
  React tab, and assets. No modules ship by default.
- OpenAPI spec generated from `swag` annotations, driving a generated
  TypeScript client.
- Release notification: at startup kopusha asks the GitHub releases
  API whether a newer version exists and, if so, the status bar links to
  its release page. `GET /api/update` exposes the result.
- Parser rule manifest: `build.sh` records the SHA-256 of every
  `parsers.d/` rule into `parsers.d.sha256`, which `go:embed` compiles
  into the binary. `--verbose` reports which rules differ from shipped.
- `THIRD-PARTY-NOTICES.md` and a CycloneDX SBOM (`sbom.cdx.json`),
  regenerated by `scripts/generate-sbom.py` and shipped with every
  release.

### Security

- Binds `127.0.0.1` by default. A non-loopback `listen` is honoured
  only when a TLS certificate is supplied; otherwise the server warns
  and falls back to loopback. There is no authentication layer — see
  [SECURITY.md](./SECURITY.md) for the threat model.
- DuckDB extensions are statically linked; kopusha never downloads
  an extension at runtime, which is what makes air-gapped use safe.
- **kopusha never downloads or executes code.** The release check
  reports a version string and nothing more, so there is no path by
  which a compromised release channel could reach an existing install.
  Signed, user-initiated updates are deferred — see
  [docs/SELF_UPDATE_PROPOSAL.md](./docs/SELF_UPDATE_PROPOSAL.md).

### Notes

- **Every dependency compiled into the binary is permissive** — MIT,
  BSD, Apache-2.0, ISC. EVTX parsing uses
  `www.velocidex.com/golang/evtx` rather than the more commonly seen
  `0xrawsec/golang-evtx`, which is GPL-3.0 and could not be statically
  linked into an MIT-licensed binary. The Velocidex parser also returns
  errors where the other panicked from inside its own goroutine, so a
  malformed `.evtx` can no longer terminate the process.
- **The release check is on by default** and is the only outbound
  request kopusha makes. It sends nothing about the host beyond what
  any HTTP request reveals. `update_check = false` in
  `kopusha.conf`, or `--no-update-check`, removes it entirely — after
  which the binary makes no network requests at all.
- **EVTX column layout** follows the Velocidex parser: the event ID is
  at `Event.System.EventID.Value`, and `EventData` entries are keyed by
  name.

[Unreleased]: https://github.com/labmk/kopusha/compare/v0.3.3...HEAD
[0.3.3]: https://github.com/labmk/kopusha/releases/tag/v0.3.3
[0.3.2]: https://github.com/labmk/kopusha/releases/tag/v0.3.2
[0.3.1]: https://github.com/labmk/kopusha/releases/tag/v0.3.1
[0.3.0]: https://github.com/labmk/kopusha/releases/tag/v0.3.0
[0.2.2]: https://github.com/labmk/kopusha/releases/tag/v0.2.2
[0.2.1]: https://github.com/labmk/kopusha/releases/tag/v0.2.1
[0.2.0]: https://github.com/labmk/kopusha/releases/tag/v0.2.0
[0.1.1]: https://github.com/labmk/kopusha/releases/tag/v0.1.1
[0.1.0]: https://github.com/labmk/kopusha/releases/tag/v0.1.0

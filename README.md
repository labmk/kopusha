# kopusha

A local viewer for log, metric and trace files.

## The problem

Someone hands you a folder of files: application logs, a Windows event
log export, some NDJSON, an XML dump. You need to find out what
happened. The files have different formats and no shared schema.

The usual options are slow to set up. A log platform needs a server, an
ingestion pipeline and an index before you can search anything. SQL
tools for local files expect data that is already structured (CSV,
JSON, Parquet), so a text log line is one string column and you end up
writing regular expressions per format. On a locked-down or offline
machine, installing any of this may not be possible at all.

## What kopusha is

kopusha is one executable file. It contains a web UI and an embedded
[DuckDB](https://duckdb.org) query engine. There is no server to set
up, no index to build, no database to install and no runtime
dependencies. It runs on your machine and reads files from your disk.

## How it works

You start the binary. It opens a page in your browser at
`http://localhost:9200`. You pick files or a folder, and kopusha
detects each file's format, parses it and loads it into an in-memory
DuckDB database. Files with different fields are queried together as
one table; a field a file does not have is empty (`NULL`) for that
file's rows.

In the UI you can:

- filter by field with a visual builder (10 operators: is, is not,
  contains, does not contain, wildcard, not wildcard, exists, does not
  exist, is one of, is not one of), or edit the query as text;
- search free text across all fields;
- filter nested JSON fields by path, for example `nodeinfo.type`;
- set a time range with presets, typed dates, or by dragging across a
  histogram of record counts over time;
- see which fields exist, how often each is filled, and their most
  common values (**Fields**), and click a value to filter on it;
- open a record in a side panel and move between records with `j`/`k`;
- export the current result to NDJSON or Parquet, optionally with a
  copy of the kopusha binary next to it;
- share a view: filters, time range, search text, sort and visible
  columns are kept in the URL fragment, so copying the address bar
  shares the view (the loaded files are not part of it);
- build a parser rule for an unknown text format from a few sample
  lines, and see why a file did not parse.

Formats it reads:

| Format | Detected by |
|--------|-------------|
| NDJSON | `.ndjson` extension, or the first line is a JSON object |
| Parquet | `PAR1` magic bytes, or `.parquet` / `.pq` extension |
| Windows EVTX | `ElfFile\0` magic bytes |
| XML | First non-whitespace character is `<`; the repeating row element is found automatically |
| Block records | A line of 20 or more dashes near the start; `Key: Value` fields between separators |
| Line-based text logs | Regex rules in `parsers.d/`; 5 rules ship, and you can add your own |

Zip archives can be browsed and their entries loaded. Details per
format are in [REQUIREMENTS.md](./REQUIREMENTS.md).

What kopusha does not do:

- It does not send your data anywhere. The UI is served from the binary
  and loads nothing from the internet.
- It makes at most two kinds of outbound request, both to GitHub: a
  release check at startup (reads the latest version number, never
  downloads), and a release download only when you press the update
  button. `update_check = false` in `kopusha.conf` turns both off;
  then kopusha makes no network requests at all.
- It does not change your input files. It writes only: exports where
  you choose, extracted zip entries in a folder next to the zip, a
  temporary NDJSON file in the system temp folder for each EVTX, XML or
  text-log load (deleted after loading), rules you save in
  `parsers.d/`, its own settings and log files next to the binary, and
  the binary, shipped rules and samples when you install an update.
- It does not keep an index or a database between runs. Loaded data
  lives in memory and is gone when the process exits.
- It has no user accounts or authentication. It listens on `127.0.0.1`
  only, unless you configure otherwise with TLS (see
  [SECURITY.md](./SECURITY.md)).
- It is not a log collector. There is no live tail, retention or
  alerting.

## Quick start

1. Download the zip for your platform from the
   [releases page](https://github.com/labmk/kopusha/releases). Each
   archive is named `kopusha-<version>-<os>-<arch>.zip`. To check where
   it came from, see [Verifying a download](./SECURITY.md#verifying-a-download).
2. Unzip it. Keep the files together: the binary, `parsers.d/`,
   `kopusha.conf`, `kopusha_mcp.conf.example` and `samples/`.
3. On Linux or macOS, if the binary does not start, restore the
   executable bit: `chmod +x kopusha`. On macOS, a downloaded binary is
   blocked until you remove the quarantine attribute:
   `xattr -d com.apple.quarantine ./kopusha`.
4. Run it:

   ```bash
   ./kopusha
   ```

   Your browser opens at `http://localhost:9200`.
5. Press **Try the samples** to load the bundled sample files, or
   **+ Add** to browse to your own. Press `?` in the UI for a reference
   of every control.

The server stops by itself when no browser tab has contacted it for
`timeout` seconds (default 180), when you close the last tab, or when
you press **Stop server**.

### Command-line options

```bash
kopusha                                            # port 9200, opens a browser
kopusha --dir /path/to/logs/                       # pre-load the .ndjson files in a folder
kopusha --files "/path/to/*.log"                   # pre-load files matching a glob, any format
kopusha --port 8080 --no-browser                   # other port, do not open a browser
kopusha --port 9443 --cert cert.pem --key key.pem  # serve over HTTPS
kopusha --verbose                                  # print startup timings
kopusha --no-update-check                          # skip the release check for this run
kopusha --version                                  # print the version and exit
kopusha mcp                                        # stdio bridge for AI agents, see docs/MCP.md
```

`--dir` only pre-loads files ending in `.ndjson`. Use `--files` with a
glob, or the file browser in the UI, for other formats.

## Supported platforms

| Platform | Release archive |
|----------|-----------------|
| Windows x86-64 | `windows-amd64` |
| Linux x86-64 | `linux-amd64` |
| Linux ARM64 | `linux-arm64` |
| macOS Apple Silicon | `darwin-arm64` |

Intel Macs are not supported. Linux builds need glibc. Release binaries
are not code-signed, so Windows SmartScreen may warn and macOS needs
the quarantine step above.

## Configuration

kopusha reads every `kopusha*.conf` file in the folder that holds the
binary, in alphabetical order. Files ending in `.example` are skipped.
All settings are optional; the shipped `kopusha.conf` has them all
commented out.

| Key | Default | Meaning |
|-----|---------|---------|
| `port` | `9200` | HTTP port. `--port` overrides it. |
| `timeout` | `180` | Seconds without any request before the server exits. `0` disables. |
| `listen` | `127.0.0.1` | Bind address. A non-loopback value is used only when `--cert` and `--key` are given; otherwise kopusha warns and stays on `127.0.0.1`. |
| `update_check` | `true` | Set to `false` to turn off the release check and the update button. |
| `parsers_dir` | `parsers.d` next to the binary | Folder of parser rules. A relative path is resolved against the current working directory, so use an absolute path. |

Example:

```ini
# Comments go on their own line. A '#' after a value is not a comment.
port = 9200
timeout = 0
update_check = false
```

Optional modules have their own file, `kopusha_<name>.conf`. A module
is on when its `[<name>]` section exists. The only shipped module is
the MCP endpoint for AI agents, off by default: see
[docs/MCP.md](./docs/MCP.md).

kopusha also writes two things next to the binary:
`kopusha_settings.json` (last opened folder, and `auto_load_previous`:
set it to `true` to reload the `.ndjson` files from that folder at
startup) and `logs/kopusha_<date>.log` (a JSON log of loads and errors).

## Adding a text format

Load a file that kopusha does not recognise and it shows what each
format adapter checked and why it declined, and the first line as the
parser saw it. From there, **Build a rule from this line** (or
**Parser rules** in the header) lets you paste sample lines, review a
proposed pattern and the rows it would produce, and save it. The saved
rule is a YAML file in `parsers.d/` and applies to the next load
without a restart.

You can also write rules by hand. The rule format is described in
[ARCHITECTURE.md](./ARCHITECTURE.md#parser-rules-parsersd). Formats that
a regular expression cannot describe need a new adapter in Go; see
[CONTRIBUTING.md](./CONTRIBUTING.md#adding-a-log-format).

## Documentation

| Document | Contents |
|----------|----------|
| [ARCHITECTURE.md](./ARCHITECTURE.md) | Components, data flow, parser rule format, HTTP API, design decisions |
| [REQUIREMENTS.md](./REQUIREMENTS.md) | Supported formats, the ingest contract and its test matrix |
| [SECURITY.md](./SECURITY.md) | Threat model, network behaviour, verifying downloads, self-update, reporting issues |
| [CONTRIBUTING.md](./CONTRIBUTING.md) | Development setup, tests, rules for pull requests and test data |
| [docs/BUILD.md](./docs/BUILD.md) | Building from source on each platform |
| [docs/MCP.md](./docs/MCP.md) | Connecting a local AI agent |
| [docs/MODULES.md](./docs/MODULES.md) | Writing an optional module |
| [docs/ROADMAP.md](./docs/ROADMAP.md) | Planned and deliberately unplanned work |
| [CHANGELOG.md](./CHANGELOG.md) | Changes per release |

## AI-assisted development

kopusha was written with AI assistance. Architecture, design decisions,
review and testing are done by people; a large part of the
implementation, documentation and test fixtures was generated with AI
tools. Read it as you would any other code you did not write before
you run it on data you care about.

## License

MIT, see [LICENSE](./LICENSE). Copyright (c) 2026 labmk.

Everything compiled into the binary is under a permissive license
(MIT, BSD, Apache-2.0, ISC). [THIRD-PARTY-NOTICES.md](./THIRD-PARTY-NOTICES.md)
lists the components and `sbom.cdx.json` is the same list in CycloneDX
format; both ship in every release archive. The software is provided
as-is, without warranty or support commitment.

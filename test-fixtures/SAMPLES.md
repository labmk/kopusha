# Sample logs

This folder ships with kopusha so a fresh install has something to
open. It is the same fixture set the test suite runs against — one file
per format the bundled `parsers.d/` rules cover — copied in by
`build.sh`.

**Every file here is synthetic.** Nothing was captured from a running
system. The service names, hosts and message bodies come from a
fictional distributed application, and the IP addresses are in
`192.0.2.0/24`, the RFC 5737 documentation range that cannot route.
Most of it is emitted by a seeded generator (`test-fixtures/generate.py`
in the repository); the rest is written by hand. Deleting this folder
affects nothing — the binary does not read it.

## What is in it

| File | Format | What it shows |
|------|--------|---------------|
| `sample.ndjson` | NDJSON | Nested objects. `service.name`, `host.name` and `nodeinfo.type` become DuckDB `STRUCT`s you can filter on by sub-path. |
| `sample.parquet` | Parquet | The columnar path — types survive the round trip, and this is what **Export** writes. |
| `xml-row-element.txt` | XML | Repeating `<Event>` rows with no declaration and no single root element; the row element is autodetected. |
| `block-keyvalue-dash.txt` | Block text | `Key: Value` records separated by a rule of dashes. Has a UTF-8 BOM and empty values, both handled. |
| `line-iso-bracket.log` | Line text | `2024-03-18 06:00:00.000 [INF] [SCH] [PID:4179] …`, with indented continuation lines folded into the record above them. |
| `line-dashdate-level.log` | Line text | `18-03-2024 06:00:00.000 Info …` — day-first dates. |
| `line-dotdate-pidtid.log` | Line text | `18.03.24 06:00:00:000 67727/611247 …` — note the colon before the milliseconds. |
| `line-time-pidtid.log` | Line text | Time of day with no date; the date comes from the file's mtime and rolls over at midnight, which this file crosses. |
| `line-time-dotdate.log` | Line text | `06:00:00.000 18.03.2024: …` — time before date. |
| `unmatched.log` | *none* | **Fails to load on purpose.** Load it to see the diagnosis: which adapters looked, what each one objected to, and the line as the parser saw it. The **Parser rules** builder opens from there. |

Windows event logs are supported but no `.evtx` sample is included —
the project's EVTX fixture is third-party data rather than something it
generates, so it stays in the repository and out of the download. Point
kopusha at any `.evtx` from your own machine to try it
(`wevtutil epl Application sample.evtx`).

## Opening them

Start kopusha, then **Open Files** (or **+ Add**) and browse to this
folder — that is the path everything else goes through, and it lets you
pick which formats to load together. Loading several at once is worth
doing: they have unrelated schemas, and queries union them and fill
`NULL` where a file has no such column.

From the command line:

```bash
kopusha --files "samples/*.log"     # the line-format samples
kopusha --dir samples/              # NDJSON only — see below
```

`--dir` pre-loads the `.ndjson` files it finds and ignores everything
else; use `--files` with a glob, or the file browser, for the rest.

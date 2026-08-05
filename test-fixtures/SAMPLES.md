# Sample logs

This folder ships with kopusha so a fresh install has something to
open. The files are copied in by `build.sh` from the fixture set the
test suite runs against.

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
| `unmatched.log` | *none* | **Fails to load on purpose.** Load it to see the diagnosis: which adapters looked, what each one objected to, and the line as the parser saw it. The **Parser rules** builder opens from there. |

The three that parse carry their own timestamps and fall inside the same
hour, so the histogram says something the moment they load. They also
have unrelated schemas, which is the point of loading them together:
queries union them and fill `NULL` where a file has no such column.

## Opening them

Start kopusha and press **Try the samples** on the empty screen, or use
**Open Files** / **+ Add** and browse to this folder.

From the command line:

```bash
kopusha --files "samples/*"     # everything here
kopusha --dir samples/          # NDJSON only — see below
```

`--dir` pre-loads the `.ndjson` files it finds and ignores everything
else; use `--files` with a glob, or the file browser, for the rest.

## What is not here

The shipped set is a first impression, not the format matrix.
`REQUIREMENTS.md` is where coverage is guaranteed, and **every supported
format keeps its `parsers.d/` rule inside the binary's install** — your
own logs in these shapes parse whether or not a sample for them ships.

**EVTX** — the fixture is a vendored capture, third-party data rather
than something this project generates. The code path is proven in CI;
point kopusha at any `.evtx` from your own machine
(`wevtutil epl Application sample.evtx`).

**Time-of-day-only line logs** (`HH:MM:SS` with no date) — supported and
tested, but the rule takes the date from the file's own mtime. That is
right for a real log sitting where it was written and wrong for a
sample: copy it without preserving timestamps, re-save it, or unpack the
archive with a tool that ignores them, and those rows jump to today
while the rest stay put. How logs like these should sit on a timeline
beside dated ones is an open question, and one better settled against
real data than against a fixture.

**The other line and block formats** — held back for now. They were in
the shipped set largely to fill the table, and a smaller set that reads
well beats a complete one that does not.

#!/usr/bin/env python3
"""Generate obs-viewer's test fixtures.

Every fixture is synthetic. Fixtures exercise *format grammar* — the
shape a parser has to handle — and nothing else. See the "Test data
policy" section in CLAUDE.md for the rules; in short, a fixture must
never contain real hostnames, real filesystem paths, vendor or product
names, or identifiers copied from any live system.

The generator is seeded, so re-running it reproduces the committed
fixtures byte for byte. Regenerate after changing a parsers.d/ rule:

    python3 test-fixtures/generate.py

Each writer targets exactly one rule in parsers.d/ (or, for NDJSON, the
direct DuckDB path). The rule its output must satisfy is named in the
docstring so a regex change and a fixture change stay in step.
"""

import json
import os
import random
import sys
from datetime import datetime, timedelta, timezone

SEED = 20240117
HERE = os.path.dirname(os.path.abspath(__file__))
FORMATS = os.path.join(HERE, "formats")
NDJSON = os.path.join(HERE, "ndjson")

# --- Synthetic vocabulary -------------------------------------------------
# A fictional distributed application. Nothing here maps to a real
# system. IPs come from RFC 5737 TEST-NET-1 (192.0.2.0/24), which is
# reserved for documentation and can never route.

SERVICES = ["gateway", "indexer", "scheduler", "blobstore", "authsvc"]
NODE_TYPES = ["edge", "indexer", "storage", "control"]
HOSTS = [f"node-{g}{i:02d}" for g in ("a", "b", "c") for i in range(1, 4)]
LEVELS = ["DEBUG", "INFO", "WARN", "ERROR"]
LEVEL_WEIGHTS = [15, 60, 18, 7]
COMPONENTS = ["CS", "IO", "NET", "DB", "SCH"]

MESSAGES = [
    "connection pool resized to {n}",
    "request completed in {n}ms",
    "cache miss for key {hex}",
    "retrying upstream after {n}ms backoff",
    "flushed {n} records to segment {hex}",
    "lease renewed, ttl {n}s",
    "dropped {n} stale entries during compaction",
    "worker {n} joined the pool",
    "checkpoint written at offset {n}",
    "shard {n} rebalanced to {host}",
    "handshake with 192.0.2.{n} completed",
    "config reloaded from profile {hex}",
    "queue depth {n} exceeds soft limit",
    "background scan finished, {n} objects visited",
]

ERROR_MESSAGES = [
    "upstream 192.0.2.{n} refused connection after 3 attempts",
    "segment {hex} failed checksum validation",
    "lease acquisition timed out after {n}ms",
    "deserialization failed at offset {n}: unexpected token",
    "write rejected: quota exceeded by {n} bytes",
]

BASE = datetime(2024, 3, 18, 6, 0, 0, tzinfo=timezone.utc)


def msg(rng, level):
    pool = ERROR_MESSAGES if level in ("ERROR", "WARN") else MESSAGES
    return rng.choice(pool).format(
        n=rng.randint(1, 9999),
        hex=f"{rng.getrandbits(32):08x}",
        host=rng.choice(HOSTS),
    )


def level(rng):
    return rng.choices(LEVELS, weights=LEVEL_WEIGHTS)[0]


def clock(rng, start, i, step_ms=(4, 900)):
    """Monotonic timestamp generator with jittered spacing."""
    return start + timedelta(milliseconds=i * rng.randint(*step_ms))


# --- Writers --------------------------------------------------------------


def write_ndjson(path, rows, rng, service):
    """REQ-DT-01 — direct DuckDB read_json_auto path.

    Includes a nested object (`nodeinfo`) so the STRUCT sub-path
    discovery and `nodeinfo.type` filtering in engine.go stay covered.
    """
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        for i in range(rows):
            ts = clock(rng, BASE, i)
            lvl = level(rng)
            rec = {
                "@timestamp": ts.strftime("%Y-%m-%dT%H:%M:%S.") + f"{ts.microsecond // 1000:03d}Z",
                "ObservedTimestamp": (ts + timedelta(milliseconds=rng.randint(1, 40)))
                .strftime("%Y-%m-%dT%H:%M:%S.") + f"{ts.microsecond // 1000:03d}Z",
                "SeverityText": lvl,
                "service": {"name": service, "version": "2.4.1"},
                "nodeinfo": {"type": rng.choice(NODE_TYPES), "id": f"n{rng.randint(1, 12):02d}"},
                "host": {"name": rng.choice(HOSTS), "ip": f"192.0.2.{rng.randint(1, 254)}"},
                "app": {
                    "name": f"{service}.worker",
                    "threadid": str(rng.randint(1000, 99999)),
                    "severity": lvl.lower(),
                },
                "message": msg(rng, lvl),
                "trace": {"id": f"{rng.getrandbits(64):016x}", "span": f"{rng.getrandbits(32):08x}"},
            }
            fh.write(json.dumps(rec) + "\n")


def write_xml_row_element(path, rows, rng):
    """REQ-DT-03 — parsers.d/10-xml-row-element.yaml.

    Repeating <Event> rows, no XML declaration and no single root (the
    adapter autodetects the row element and streams multi-root input).
    Timestamp comes from the `time` attribute, matching `@time` in the
    rule's ts_candidates. Includes an escaped-entity payload so the
    decoder path stays exercised.
    """
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        for i in range(rows):
            ts = clock(rng, BASE, i, (50, 2000))
            lvl = level(rng)
            fh.write(
                f'<Event type="Telemetry" name="Checkpoint" '
                f'time="{ts.strftime("%Y-%m-%d %H:%M:%SZ")}">\n'
                f"<Version>1.0</Version>\n"
                f"<Functionality>{rng.choice(SERVICES)}_Checkpoint</Functionality>\n"
                f"<ProcessName>{rng.choice(SERVICES)}.worker</ProcessName>\n"
                f"<ProcessId>{rng.randint(1000, 99999)}</ProcessId>\n"
                f"<Hostname>{rng.choice(HOSTS)}</Hostname>\n"
                f"<Severity>{lvl}</Severity>\n"
                f"<ContextId>{rng.getrandbits(64):016x}</ContextId>\n"
                f"<Detail>{escape(msg(rng, lvl))}</Detail>\n"
                f'<Duration unit="ms">{rng.randint(1, 4000)}</Duration>\n'
                f"</Event>\n"
            )


def escape(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def write_block_keyvalue(path, rows, rng):
    """REQ-DT-04 — parsers.d/00-block-keyvalue-dash.yaml.

    Records separated by a line of 40 dashes (the rule needs 20+),
    fields as `Key: Value`. `Timestamp` is in the rule's ts_field list
    and uses the RFC3339-with-offset layout it expects. Deliberately
    includes an empty value and a UTF-8 BOM, both of which the adapter
    handles.
    """
    sep = "-" * 40
    with open(path, "w", encoding="utf-8-sig", newline="\n") as fh:
        for i in range(rows):
            ts = clock(rng, BASE, i, (200, 5000))
            lvl = level(rng)
            svc = rng.choice(SERVICES)
            fh.write(sep + "\n")
            fh.write(f"Message: {msg(rng, lvl)}\n")
            fh.write(f"ID: {rng.getrandbits(32):08x}\n")
            fh.write(f"Severity: {'Information' if lvl == 'INFO' else lvl.title()}\n")
            fh.write(f"Timestamp: {ts.strftime('%Y-%m-%dT%H:%M:%S.%f')[:-3]}+01:00\n")
            fh.write(f"Process Name: /opt/{svc}/bin/{svc}-worker\n")
            fh.write(f"Process Id: {rng.randint(1000, 99999)}\n")
            fh.write("Thread Name: \n")
            fh.write(f"Thread Id: {rng.randint(1000, 99999)}\n")
            fh.write(f"AppDomain: {svc}.worker\n")
            fh.write(f"Machine: {rng.choice(HOSTS)}\n")
            fh.write("EventSource: \n")


def write_line_iso_bracket(path, rows, rng):
    """REQ-DT-05 — parsers.d/20-line-iso-bracket.yaml.
    `YYYY-MM-DD HH:MM:SS.mmm [LVL] [COMP] [PID:N] message`

    Every 30th record emits an unmatched continuation line, which the
    adapter must append to the previous record's message rather than
    drop or mis-parse.
    """
    lvl3 = {"DEBUG": "DBG", "INFO": "INF", "WARN": "WRN", "ERROR": "ERR"}
    pid = rng.randint(1000, 9999)
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        for i in range(rows):
            ts = clock(rng, BASE, i, (4, 400))
            lvl = level(rng)
            fh.write(
                f"{ts.strftime('%Y-%m-%d %H:%M:%S.')}{ts.microsecond // 1000:03d} "
                f"[{lvl3[lvl]}] [{rng.choice(COMPONENTS)}] [PID:{pid}] {msg(rng, lvl)}\n"
            )
            if i % 30 == 29:
                fh.write("    at frame " + f"{rng.getrandbits(32):08x}" + " (continuation)\n")


def write_line_dashdate_level(path, rows, rng):
    """REQ-DT-06 — parsers.d/21-line-dashdate-level.yaml.
    `DD-MM-YYYY HH:MM:SS.mmm Level message`
    """
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        for i in range(rows):
            ts = clock(rng, BASE, i, (4, 400))
            lvl = level(rng)
            fh.write(
                f"{ts.strftime('%d-%m-%Y %H:%M:%S.')}{ts.microsecond // 1000:03d} "
                f"{lvl.title():<7} {msg(rng, lvl)}\n"
            )


def write_line_dotdate_pidtid(path, rows, rng):
    """REQ-DT-07 — parsers.d/22-line-dotdate-pidtid.yaml.
    `prefix: DD.MM.YY HH:MM:SS:msec PID/TID message`

    Note the colon before the milliseconds — Go's time layout language
    can't express that, so the rule rewrites it via ts_regex_subs before
    time.Parse. Keep the colon; it is the point of this fixture.
    """
    pid = rng.randint(10000, 99999)
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        for i in range(rows):
            ts = clock(rng, BASE, i, (10, 800))
            lvl = level(rng)
            svc = rng.choice(SERVICES)
            fh.write(
                f"app.Common.{svc.title()}Service: "
                f"{ts.strftime('%d.%m.%y %H:%M:%S:')}{ts.microsecond // 1000:03d} "
                f"{pid}/{rng.randint(100000, 999999)} {msg(rng, lvl)}\n"
            )


def write_line_time_pidtid(path, rows, rng):
    """REQ-DT-08 — parsers.d/23-line-time-pidtid.yaml.
    `HH:MM:SS.uuuuuu PID/TID Level message`

    Time-of-day only; the adapter sources the date from the file mtime
    and advances a day when the clock jumps backwards by >12h. This
    fixture crosses midnight on purpose so the rollover branch is
    covered end to end.
    """
    pid = rng.randint(10000, 99999)
    start = datetime(2024, 3, 18, 23, 40, 0, tzinfo=timezone.utc)
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        for i in range(rows):
            ts = start + timedelta(milliseconds=i * rng.randint(200, 3000))
            lvl = level(rng)
            fh.write(
                f"{ts.strftime('%H:%M:%S.%f')} "
                f"{pid}/{rng.randint(10000, 99999)} {lvl.title():<6} {msg(rng, lvl)}\n"
            )


def write_line_time_dotdate(path, rows, rng):
    """REQ-DT-09 — parsers.d/24-line-time-dotdate.yaml.
    `HH:MM:SS.mmm DD.MM.YYYY: message` — time before date.
    """
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        for i in range(rows):
            ts = clock(rng, BASE, i, (100, 4000))
            lvl = level(rng)
            fh.write(
                f"{ts.strftime('%H:%M:%S.')}{ts.microsecond // 1000:03d} "
                f"{ts.strftime('%d.%m.%Y')}: {msg(rng, lvl)}\n"
            )


def main():
    os.makedirs(FORMATS, exist_ok=True)
    os.makedirs(NDJSON, exist_ok=True)

    # One RNG per file, each seeded from the master seed, so adding a
    # new fixture doesn't shift the contents of every existing one.
    def rng_for(name):
        return random.Random(f"{SEED}:{name}")

    jobs = [
        ("formats/sample.ndjson", lambda p: write_ndjson(p, 400, rng_for(p), "gateway")),
        ("formats/xml-row-element.txt", lambda p: write_xml_row_element(p, 300, rng_for(p))),
        ("formats/block-keyvalue-dash.txt", lambda p: write_block_keyvalue(p, 250, rng_for(p))),
        ("formats/line-iso-bracket.log", lambda p: write_line_iso_bracket(p, 1200, rng_for(p))),
        ("formats/line-dashdate-level.log", lambda p: write_line_dashdate_level(p, 1200, rng_for(p))),
        ("formats/line-dotdate-pidtid.log", lambda p: write_line_dotdate_pidtid(p, 800, rng_for(p))),
        ("formats/line-time-pidtid.log", lambda p: write_line_time_pidtid(p, 600, rng_for(p))),
        ("formats/line-time-dotdate.log", lambda p: write_line_time_dotdate(p, 800, rng_for(p))),
        # Pre-loaded by playwright.config.js for smoke.spec.js. Two files
        # with overlapping-but-not-identical schemas so the UNION ALL
        # column-merge path is exercised by the smoke run.
        ("ndjson/gateway.ndjson", lambda p: write_ndjson(p, 2500, rng_for(p), "gateway")),
        ("ndjson/indexer.ndjson", lambda p: write_ndjson(p, 2500, rng_for(p), "indexer")),
    ]

    for rel, fn in jobs:
        path = os.path.join(HERE, rel)
        fn(path)
        print(f"{os.path.getsize(path):>9,} B  {rel}")

    print(
        "\nNote: test-fixtures/formats/sample.evtx is NOT generated here.\n"
        "EVTX is a binary format with per-chunk CRCs; see REQUIREMENTS.md\n"
        "(REQ-DT-02) for how that fixture is sourced.",
        file=sys.stderr,
    )


if __name__ == "__main__":
    main()

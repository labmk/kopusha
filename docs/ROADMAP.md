# Roadmap

Direction, not dates. This is a single-maintainer project — items move
when someone does the work, and "future" honestly means "wanted, not
scheduled".

Tracking lives in [issues](https://github.com/labmk/kopusha/issues),
grouped by [milestone](https://github.com/labmk/kopusha/milestones)
and tagged `horizon:*`. This file explains the *reasoning*; the issues
carry the detail.

## Where the project is aimed

Querying local files with SQL is a solved problem, and well served.
Competing on "query files in a browser" means fighting for ground that
is already held.

What those tools do not do is **turn unstructured logs into typed
columns** — they start from Parquet, CSV or JSON. The `parsers.d/` rule
system, the confidence-scored format dispatch, and the union across
files with unrelated schemas are the part of this project with no
ready equivalent.

So the direction is: **invest in parsing and in the ergonomics around
it; treat the rest of the analytical ecosystem as an ally rather than a
competitor.** Parquet export is the clearest expression of that —
parse the mess here, hand the result to whatever the user already
likes.

The audience is explicitly *not* people who are comfortable with grep
and a regex. They are already served. It is people who need the answers
and do not write regex for fun.

## Do it now

Empty. Everything that was here shipped in 0.2.0 — see the
[changelog](../CHANGELOG.md):

- **#13 rule-authoring UI** and **#14 explain why a file did not
  parse**, which turned out to be one feature from two directions: the
  failure message is the entry point to the rule builder.
- **#19 time histogram** and **#20 shareable query state**, which were
  small and independent of both.
- **#12 virtualised result table**, **#15 Parquet read and write**, and
  **#25 export writing where you are actually looking**.

The next thing to pick up is #18.

## Near future

The next substantive capabilities.

| | |
|---|---|
| [#18](https://github.com/labmk/kopusha/issues/18) | **Field profiling panel.** Answers "what is in this data" before a query is written. |

## Future

Wanted, not scheduled.

| | |
|---|---|
| [#16](https://github.com/labmk/kopusha/issues/16) | **MCP server.** An agent cannot parse heterogeneous logs; that is exactly what this project has. The API already exists. |
| [#17](https://github.com/labmk/kopusha/issues/17) | **User-initiated update.** A button that fetches and replaces the binary, then restarts. Blocked on signing. |
| [#24](https://github.com/labmk/kopusha/issues/24) | **Sign releases.** Gates #17, and is what would let macOS builds be notarized so a download stops being blocked. |
| [#21](https://github.com/labmk/kopusha/issues/21) | **Remote sources.** NFS and SMB already work as mounted paths and need documenting, not building. S3 needs `httpfs`, which is not statically linked — and loading it at runtime would cost the air-gap guarantee. |

## Far future

Blocked on a precondition or a decision.

| | |
|---|---|
| [#22](https://github.com/labmk/kopusha/issues/22) | **Streaming / live tail.** A second mode with its own state machine, in a space that is already well served. |
| [#23](https://github.com/labmk/kopusha/issues/23) | **LLM-assisted query building.** Conflicts with air-gapped operation, and the DSL is probably not the bottleneck. Ship field profiling first and see whether the need survives. |

## Things deliberately not planned

- **Becoming an observability platform.** Ingest, retention, alerting,
  dashboards. Mature tools do that, and better than a project this size
  could.
- **A TUI.** Long-established tools occupy that ground with far more
  work behind them than this project would put in.
- **Authentication.** The loopback bind *is* the access control; see
  [SECURITY.md](../SECURITY.md). Anything else belongs behind a
  reverse proxy.

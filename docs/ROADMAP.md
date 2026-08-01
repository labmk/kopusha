# Roadmap

Direction, not dates. This is a single-maintainer project — items move
when someone does the work, and "future" honestly means "wanted, not
scheduled".

Tracking lives in [issues](https://github.com/labmk/obs-viewer/issues),
grouped by [milestone](https://github.com/labmk/obs-viewer/milestones)
and tagged `horizon:*`. This file explains the *reasoning*; the issues
carry the detail.

## Where the project is aimed

Querying local files with SQL is a solved problem. DuckDB ships its own
browser UI; Rill is a single binary with an embedded DuckDB and a better
exploration experience. Competing on "query files in a browser" means
fighting for ground that is already held.

What none of them do is **turn unstructured logs into typed columns**.
They all start from Parquet, CSV or JSON. The `parsers.d/` rule system,
the confidence-scored format dispatch, and the union across files with
unrelated schemas are the part of this project that has no equivalent.

So the direction is: **invest in parsing and in the ergonomics around
it; treat the rest of the analytical ecosystem as an ally rather than a
competitor.** Exporting Parquet (#15) is the clearest expression of
that — parse the mess here, hand the result to whatever the user
already likes.

The audience is explicitly *not* people who are comfortable with grep
and a regex. They are already served. It is people who need the answers
and do not write regex for fun.

## Do it now

Gaps in what already ships.

| | |
|---|---|
| [#12](https://github.com/labmk/obs-viewer/issues/12) | **Virtualise the result table.** Every row of a page is in the DOM; the page size goes to 10,000. |
| [#13](https://github.com/labmk/obs-viewer/issues/13) | **Rule-authoring UI.** The best capability in the product is currently gated behind hand-writing regex into YAML. |
| [#14](https://github.com/labmk/obs-viewer/issues/14) | **Explain why a file did not parse.** The detection scores exist and are discarded. |

#13 and #14 are the same feature from two directions: the failure
message should be the entry point to the rule builder.

## Near future

The next substantive capabilities.

| | |
|---|---|
| [#15](https://github.com/labmk/obs-viewer/issues/15) | **Parquet export.** The extension is already statically linked. Repositions the project as a front-end to the DuckDB ecosystem. |
| [#16](https://github.com/labmk/obs-viewer/issues/16) | **MCP server.** An agent cannot parse heterogeneous logs; that is exactly what this project has. The API already exists. |
| [#17](https://github.com/labmk/obs-viewer/issues/17) | **User-initiated update**, Notepad++ style. Blocked on release signing. |
| [#18](https://github.com/labmk/obs-viewer/issues/18) | **Field profiling panel.** Answers "what is in this data" before a query is written. |
| [#19](https://github.com/labmk/obs-viewer/issues/19) | **Time histogram strip.** Turns time filtering from typing into gesture. |
| [#20](https://github.com/labmk/obs-viewer/issues/20) | **Shareable query state in the URL.** How a tool spreads inside a team. |

## Future

Wanted, not scheduled.

| | |
|---|---|
| [#21](https://github.com/labmk/obs-viewer/issues/21) | **Remote sources.** NFS and SMB already work as mounted paths and need documenting, not building. S3 needs `httpfs`, which is not statically linked — and loading it at runtime would cost the air-gap guarantee. |
| [#22](https://github.com/labmk/obs-viewer/issues/22) | **Streaming / live tail.** A second mode with its own state machine, in a space where Logdy already does it well. |

## Far future

Blocked on a precondition or a decision.

| | |
|---|---|
| [#23](https://github.com/labmk/obs-viewer/issues/23) | **LLM-assisted query building.** Conflicts with air-gapped operation, and the DSL is probably not the bottleneck. Ship field profiling first and see whether the need survives. |
| — | **Release signing.** Not an issue yet because it is a decision rather than a task: which scheme, where the private key lives, who can use it. It gates #17, and it is what would let macOS builds be notarized. |

## Things deliberately not planned

- **Becoming an observability platform.** Ingest, retention, alerting,
  dashboards — VictoriaLogs, OpenObserve and Parseable do that, and do
  it better than a project this size could.
- **A TUI.** lnav occupies that ground with fifteen years of work and a
  better implementation than this project would produce.
- **Authentication.** The loopback bind *is* the access control; see
  [SECURITY.md](../SECURITY.md). Anything else belongs behind a
  reverse proxy.
